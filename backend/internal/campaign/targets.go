package campaign

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/compose"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Turning what an operator picked into a list the queue can run.
//
// Four things happen here, in this order, and the order matters:
//
//	 1. Every entry is normalised to a WhatsApp address. A number typed as
//	    "0812-3456-7890", "+62 812 3456 7890" and "628123456789" is one person.
//	 2. The list is deduplicated. One recipient receives one campaign once,
//	    however many routes they arrived by — a pasted list, an imported file and
//	    a group's membership may all contain the same customer.
//	 3. Each recipient is assigned to a device that can actually reach them,
//	    round-robin so the load is even.
//	 4. The message is rendered per recipient, so the review screen shows the
//	    real text rather than a template.
//
// Anything that fails one of those steps is reported with a reason an operator
// can act on, rather than silently dropped.

// digits strips everything that is not a digit.
var digits = regexp.MustCompile(`\D`)

// NormalizeMSISDN turns a typed number into WhatsApp's own form.
//
// Indonesian conventions are handled explicitly because that is what this
// workspace types: a leading 0 is the national trunk prefix and becomes 62, a
// leading + or 00 is an international prefix and is simply dropped. A number
// already in international form is left alone — including a non-Indonesian one,
// since nothing here should refuse a Malaysian customer.
func NormalizeMSISDN(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("kosong")
	}
	// Somebody pasting a JID straight in is doing something reasonable.
	if idx := strings.Index(trimmed, "@"); idx > 0 {
		trimmed = trimmed[:idx]
	}

	national := strings.HasPrefix(trimmed, "0") && !strings.HasPrefix(trimmed, "00")
	only := digits.ReplaceAllString(trimmed, "")

	switch {
	case only == "":
		return "", fmt.Errorf("tidak mengandung angka")
	case national:
		only = "62" + strings.TrimLeft(only, "0")
	case strings.HasPrefix(only, "00"):
		only = strings.TrimPrefix(only, "00")
	}

	// A country code plus a subscriber number. Shorter is a typo or a service
	// short code; longer is not a phone number at all.
	if len(only) < 8 || len(only) > 15 {
		return "", fmt.Errorf("panjang nomor tidak wajar (%d digit)", len(only))
	}
	return only, nil
}

// personalJID builds the one-to-one address for a normalised number.
func personalJID(msisdn string) string { return msisdn + "@s.whatsapp.net" }

// TargetRequest is what the composer asked for.
type TargetRequest struct {
	WorkspaceID uuid.UUID
	Scope       repository.Scope
	AccountIDs  []uuid.UUID
	// Source is manual, csv, contacts, group_members or groups.
	Source string
	// Numbers carries pasted or imported entries, one per line or cell.
	Numbers []string
	// GroupIDs are conversation ids, for the group and group-member sources.
	GroupIDs []uuid.UUID
	// ContactIDs narrows the contacts source; empty means every contact the
	// selected devices know.
	ContactIDs []uuid.UUID

	Template     string
	ComposeMode  string
	DelayProfile string
	// ApplicationCode fills the {{aplikasi}} placeholder.
	ApplicationCode string
	// CustomValues are the workspace variables' values for this campaign, from
	// the composer. Applied to every recipient.
	CustomValues map[string]string
}

// Resolver turns a request into a plan plus the rows to write.
type Resolver struct {
	repo   *repository.Repo
	online func(uuid.UUID) bool
}

// NewResolver builds one. `online` reports whether a device is connected; it is
// injected rather than taken from the manager so the rule can be tested without
// a WhatsApp session.
func NewResolver(repo *repository.Repo, online func(uuid.UUID) bool) *Resolver {
	if online == nil {
		online = func(uuid.UUID) bool { return true }
	}
	return &Resolver{repo: repo, online: online}
}

// Resolve produces the review plan and the recipient rows behind it.
//
// Both come from one pass, so the numbers the operator approves on the review
// screen are the numbers that get written — not a second calculation that might
// disagree.
func (rv *Resolver) Resolve(
	ctx context.Context, req TargetRequest,
) (*models.TargetPlan, []models.ResolvedTarget, error) {
	accounts, err := rv.repo.AccountsByIDs(ctx, req.Scope, req.AccountIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(accounts) == 0 {
		return nil, nil, fmt.Errorf("tidak ada perangkat pengirim yang dapat dipakai")
	}
	accountIDs := make([]uuid.UUID, 0, len(accounts))
	for _, a := range accounts {
		accountIDs = append(accountIDs, a.ID)
	}

	candidates, problems, duplicates, err := rv.collect(ctx, req, accountIDs)
	if err != nil {
		return nil, nil, err
	}

	// Every slice starts empty, never nil.
	//
	// A nil slice marshals to `null`, not `[]`, and the review screen reads
	// `.length` on all four. So a broadcast with nothing wrong with it — no
	// rejected numbers, no missing variables, a clean template — sent back three
	// nulls and crashed the page that was meant to confirm it. The better the
	// campaign, the more certain the failure.
	//
	// Fixed here rather than only in the browser: the API says these fields are
	// arrays, and every future client would have to work around it otherwise.
	plan := &models.TargetPlan{
		Devices:          []models.DevicePlan{},
		Duplicates:       duplicates,
		Problems:         problems,
		Preview:          []string{},
		MissingVariables: []string{},
		TemplateProblems: []string{},
		DelayProfile:     req.DelayProfile,
	}
	if plan.Problems == nil {
		plan.Problems = []models.TargetProblem{}
	}
	if tp := compose.Validate(req.Template); len(tp) > 0 {
		plan.TemplateProblems = tp
	}

	// Devices in a stable order, so round-robin is reproducible and the review
	// screen matches what will happen.
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
	byID := map[uuid.UUID]repository.AccountInfo{}
	rotation := make([]uuid.UUID, 0, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
		rotation = append(rotation, a.ID)
	}

	assigned := map[uuid.UUID]int{}
	resolved := make([]models.ResolvedTarget, 0, len(candidates))
	missing := map[string]bool{}
	rng := rand.New(rand.NewSource(1)) // deterministic preview
	cursor := 0

	for _, c := range candidates {
		device, ok := rv.assign(c, rotation, &cursor)
		if !ok {
			plan.Unreachable++
			plan.Problems = append(plan.Problems, models.TargetProblem{
				Input:  displayOf(c),
				Reason: "tidak ada perangkat terpilih yang dapat menghubungi tujuan ini",
			})
			continue
		}

		vars := rv.variables(c, byID[device], req)
		built, err := compose.Build(req.Template, vars, rng)
		if err != nil {
			plan.Problems = append(plan.Problems, models.TargetProblem{
				Input:  displayOf(c),
				Reason: err.Error(),
			})
			continue
		}
		for _, k := range built.Missing {
			missing[k] = true
		}

		assigned[device]++
		resolved = append(resolved, models.ResolvedTarget{
			ChatJID:        c.ChatJID,
			PhoneNumber:    c.PhoneNumber,
			DisplayName:    c.Name,
			ContactID:      c.ContactID,
			ConversationID: c.ConversationID,
			TargetType:     targetTypeFor(c.Kind, req.Source),
			AccountID:      device,
			Variables:      vars,
			RenderedBody:   built.Body,
		})
		if len(plan.Preview) < 3 {
			plan.Preview = append(plan.Preview, built.Body)
		}
	}

	for _, id := range rotation {
		a := byID[id]
		plan.Devices = append(plan.Devices, models.DevicePlan{
			AccountID:   a.ID,
			AccountName: a.Name,
			PhoneNumber: a.PhoneNumber,
			Connected:   rv.online(a.ID),
			Assigned:    assigned[a.ID],
		})
	}

	plan.Valid = len(resolved)
	plan.Invalid = len(plan.Problems)
	for k := range missing {
		plan.MissingVariables = append(plan.MissingVariables, k)
	}
	sort.Strings(plan.MissingVariables)
	plan.EstimatedSeconds = estimateSeconds(req.DelayProfile, assigned)

	return plan, resolved, nil
}

// collect gathers candidates for the chosen source, deduplicated by address.
func (rv *Resolver) collect(
	ctx context.Context, req TargetRequest, accountIDs []uuid.UUID,
) (out []repository.TargetCandidate, problems []models.TargetProblem, duplicates int, err error) {
	seen := map[string]bool{}
	keep := func(c repository.TargetCandidate) {
		// Deduplication happens on the WhatsApp address, which is the only thing
		// that identifies a recipient across every source.
		if seen[c.ChatJID] {
			duplicates++
			return
		}
		seen[c.ChatJID] = true
		out = append(out, c)
	}

	// fromNumbers turns pasted or picked numbers into candidates, naming each one
	// from the address book where it is already known.
	fromNumbers := func() error {
		known, err := rv.lookupPasted(ctx, req, accountIDs)
		if err != nil {
			return err
		}
		for _, raw := range req.Numbers {
			entry := strings.TrimSpace(raw)
			if entry == "" {
				continue
			}
			msisdn, err := NormalizeMSISDN(entry)
			if err != nil {
				problems = append(problems, models.TargetProblem{Input: entry, Reason: err.Error()})
				continue
			}
			jid := personalJID(msisdn)
			if c, ok := known[jid]; ok {
				c.PhoneNumber = msisdn
				keep(c)
				continue
			}
			keep(repository.TargetCandidate{
				ChatJID:     jid,
				PhoneNumber: msisdn,
				Name:        msisdn,
				Kind:        "personal",
			})
		}
		return nil
	}

	switch req.Source {
	case models.TargetSourceManual, models.TargetSourceCSV:
		if err := fromNumbers(); err != nil {
			return nil, nil, 0, err
		}

	case models.TargetSourceContacts:
		found, err := rv.repo.ContactTargets(ctx, req.WorkspaceID, accountIDs, 0)
		if err != nil {
			return nil, nil, 0, err
		}
		wanted := map[uuid.UUID]bool{}
		for _, id := range req.ContactIDs {
			wanted[id] = true
		}
		for _, c := range found {
			if len(wanted) > 0 && (c.ContactID == nil || !wanted[*c.ContactID]) {
				continue
			}
			keep(c)
		}

	case models.TargetSourceGroupMembers:
		// Members ticked one at a time arrive as their numbers, because that is
		// what the screen actually chose: a group of nine hundred where eleven
		// people were selected is eleven recipients, and re-deriving them from
		// the group id would send to all nine hundred.
		//
		// Numbers win over group ids when both are present. The ids are sent
		// along as provenance, not as a second instruction.
		if len(req.Numbers) > 0 {
			if err := fromNumbers(); err != nil {
				return nil, nil, 0, err
			}
			break
		}
		if len(req.GroupIDs) == 0 {
			return nil, nil, 0, fmt.Errorf("pilih minimal satu grup atau satu anggota")
		}
		found, err := rv.repo.GroupMemberTargets(ctx, req.WorkspaceID, req.GroupIDs)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, c := range found {
			keep(c)
		}

	case models.TargetSourceGroups:
		found, err := rv.repo.GroupTargets(ctx, req.WorkspaceID, accountIDs)
		if err != nil {
			return nil, nil, 0, err
		}
		wanted := map[uuid.UUID]bool{}
		for _, id := range req.GroupIDs {
			wanted[id] = true
		}
		for _, c := range found {
			if len(wanted) > 0 && (c.ConversationID == nil || !wanted[*c.ConversationID]) {
				continue
			}
			keep(c)
		}

	default:
		return nil, nil, 0, fmt.Errorf("sumber target tidak dikenal: %q", req.Source)
	}

	return out, problems, duplicates, nil
}

func (rv *Resolver) lookupPasted(
	ctx context.Context, req TargetRequest, accountIDs []uuid.UUID,
) (map[string]repository.TargetCandidate, error) {
	jids := make([]string, 0, len(req.Numbers))
	for _, raw := range req.Numbers {
		if msisdn, err := NormalizeMSISDN(raw); err == nil {
			jids = append(jids, personalJID(msisdn))
		}
	}
	if len(jids) == 0 {
		return map[string]repository.TargetCandidate{}, nil
	}
	return rv.repo.LookupChats(ctx, req.WorkspaceID, accountIDs, jids)
}

// assign picks the device that will send to one recipient.
//
// A group can only be posted to by a number that is in it, so its own device is
// the only choice — reassigning it round-robin would produce a send that is
// guaranteed to fail. A one-to-one recipient can be reached by any connected
// number, so those rotate evenly.
func (rv *Resolver) assign(
	c repository.TargetCandidate, rotation []uuid.UUID, cursor *int,
) (uuid.UUID, bool) {
	if c.Kind == "group" {
		for _, id := range rotation {
			if id == c.AccountID {
				return id, true
			}
		}
		return uuid.Nil, false
	}

	// Round-robin over connected devices. An offline device is skipped rather
	// than handed a share that would sit idle; if none is connected the campaign
	// still records the assignment against the first device, because a phone
	// that is offline now will very likely be back before the queue drains.
	for i := 0; i < len(rotation); i++ {
		id := rotation[(*cursor+i)%len(rotation)]
		if rv.online(id) {
			*cursor = (*cursor + i + 1) % len(rotation)
			return id, true
		}
	}
	if len(rotation) == 0 {
		return uuid.Nil, false
	}
	id := rotation[*cursor%len(rotation)]
	*cursor = (*cursor + 1) % len(rotation)
	return id, true
}

// variables builds the placeholder values for one recipient.
//
// Built-ins come from the data; the workspace's own variables come from the
// composer and are the same for everybody in the campaign. A custom variable is
// never allowed to overwrite a built-in — {{nama}} has to keep meaning the
// contact's name, or a message could greet every customer by the same name.
func (rv *Resolver) variables(
	c repository.TargetCandidate, device repository.AccountInfo, req TargetRequest,
) map[string]string {
	vars := map[string]string{}
	for k, v := range req.CustomValues {
		vars[strings.ToLower(strings.TrimSpace(k))] = v
	}

	vars["nama"] = c.Name
	vars["nomor"] = c.PhoneNumber
	if vars["nomor"] == "" {
		vars["nomor"] = strings.SplitN(c.ChatJID, "@", 2)[0]
	}
	vars["aplikasi"] = req.ApplicationCode
	if c.Kind == "group" {
		vars["nama_grup"] = c.Name
	}
	_ = device
	return vars
}

func targetTypeFor(kind, source string) string {
	switch {
	case kind == "group":
		return "group"
	case kind == "group_member":
		return "group_member"
	case source == models.TargetSourceCSV:
		return "csv"
	case source == models.TargetSourceManual:
		return "manual"
	default:
		return "contact"
	}
}

func displayOf(c repository.TargetCandidate) string {
	if c.PhoneNumber != "" {
		return c.PhoneNumber
	}
	if c.Name != "" {
		return c.Name
	}
	return c.ChatJID
}

// estimateSeconds is how long the campaign should take.
//
// Devices send in parallel, so the campaign lasts as long as its busiest device
// rather than as long as the sum. The midpoint of the delay range is used, and
// the first message on each device does not wait.
func estimateSeconds(profile string, assigned map[uuid.UUID]int) int {
	rng := models.DelayProfile(profile)
	mid := (rng.Min + rng.Max) / 2

	busiest := 0
	for _, n := range assigned {
		if n > busiest {
			busiest = n
		}
	}
	if busiest <= 1 {
		return 0
	}
	return int(mid.Seconds()) * (busiest - 1)
}
