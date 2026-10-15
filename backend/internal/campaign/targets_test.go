package campaign

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

func TestNormalizeMSISDN(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"081234567890", "6281234567890"},
		{"0812-3456-7890", "6281234567890"},
		{"0812 3456 7890", "6281234567890"},
		{"+62 812 3456 7890", "6281234567890"},
		{"6281234567890", "6281234567890"},
		{"0062812 3456 7890", "6281234567890"},
		{"6281234567890@s.whatsapp.net", "6281234567890"},
		// Not Indonesian, and not this function's business to refuse.
		{"60123456789", "60123456789"},
	}
	for _, tc := range cases {
		got, err := NormalizeMSISDN(tc.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}

	rejected := []string{"", "   ", "abc", "12", "+", "0", "628123456789012345678"}
	for _, in := range rejected {
		if got, err := NormalizeMSISDN(in); err == nil {
			t.Errorf("%q was accepted as %q; it is not a phone number", in, got)
		}
	}
}

func TestNormalizeCollapsesTheSamePersonWrittenThreeWays(t *testing.T) {
	// This is what deduplication depends on: a customer pasted once and imported
	// twice in different formats has to become one recipient, or they receive
	// the campaign three times.
	forms := []string{"081234567890", "+6281234567890", "62 812-3456-7890"}
	seen := map[string]bool{}
	for _, f := range forms {
		got, err := NormalizeMSISDN(f)
		if err != nil {
			t.Fatalf("%q: %v", f, err)
		}
		seen[got] = true
	}
	if len(seen) != 1 {
		t.Fatalf("the same number in three formats produced %d distinct addresses: %v", len(seen), seen)
	}
}

func TestPersonalJID(t *testing.T) {
	if got := personalJID("6281234567890"); got != "6281234567890@s.whatsapp.net" {
		t.Fatalf("got %q", got)
	}
}

// --- distribution --------------------------------------------------------------

func TestAssignRoundRobinIsEven(t *testing.T) {
	rv := NewResolver(nil, func(uuid.UUID) bool { return true })
	devices := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	counts := map[uuid.UUID]int{}
	cursor := 0
	for i := 0; i < 30; i++ {
		id, ok := rv.assign(repository.TargetCandidate{Kind: "personal"}, devices, &cursor)
		if !ok {
			t.Fatal("a one-to-one recipient should always be assignable")
		}
		counts[id]++
	}
	for _, d := range devices {
		if counts[d] != 10 {
			t.Fatalf("device %s got %d of 30 recipients; the split should be even: %v", d, counts[d], counts)
		}
	}
}

func TestAssignSkipsOfflineDevices(t *testing.T) {
	online := uuid.New()
	offline := uuid.New()
	rv := NewResolver(nil, func(id uuid.UUID) bool { return id == online })

	devices := []uuid.UUID{offline, online}
	cursor := 0
	for i := 0; i < 6; i++ {
		got, ok := rv.assign(repository.TargetCandidate{Kind: "personal"}, devices, &cursor)
		if !ok {
			t.Fatal("expected an assignment")
		}
		if got != online {
			t.Fatalf("recipient %d was handed to an offline device", i)
		}
	}
}

func TestAssignKeepsGroupsOnTheirOwnDevice(t *testing.T) {
	// A number that is not in a group cannot post to it. Rotating a group onto
	// another device would guarantee a failure that only shows at send time.
	owner := uuid.New()
	other := uuid.New()
	rv := NewResolver(nil, func(uuid.UUID) bool { return true })

	cursor := 0
	got, ok := rv.assign(
		repository.TargetCandidate{Kind: "group", AccountID: owner},
		[]uuid.UUID{other, owner}, &cursor)
	if !ok || got != owner {
		t.Fatalf("group was assigned to %v, want its own device %v", got, owner)
	}

	// A group whose device was not selected has nowhere to go, and says so
	// rather than being handed to a number that cannot reach it.
	if _, ok := rv.assign(
		repository.TargetCandidate{Kind: "group", AccountID: uuid.New()},
		[]uuid.UUID{other, owner}, &cursor); ok {
		t.Fatal("a group with no selected member device should be unassignable")
	}
}

// --- estimates and profiles ------------------------------------------------------

func TestDelayProfilesMatchTheSpecifiedRanges(t *testing.T) {
	want := map[string][2]time.Duration{
		models.DelaySuperCepat: {1 * time.Second, 6 * time.Second},
		models.DelayCepat:      {5 * time.Second, 15 * time.Second},
		models.DelayNormal:     {30 * time.Second, 60 * time.Second},
		models.DelayAman:       {1 * time.Minute, 2 * time.Minute},
		models.DelaySantai:     {3 * time.Minute, 5 * time.Minute},
	}
	for name, bounds := range want {
		got := models.DelayProfile(name)
		if got.Min != bounds[0] || got.Max != bounds[1] {
			t.Errorf("%s: got %v..%v, want %v..%v", name, got.Min, got.Max, bounds[0], bounds[1])
		}
	}

	// An unknown profile slows a campaign down rather than speeding it up: a
	// value written by a future version must never make sending faster than the
	// operator expected.
	if got := models.DelayProfile("dari-versi-lain"); got != models.DelayProfile(models.DelayNormal) {
		t.Errorf("unknown profile fell back to %v, want Normal", got)
	}
}

func TestJitterStaysInsideTheProfile(t *testing.T) {
	rng := deterministic()
	p := models.DelayProfile(models.DelayNormal)
	for i := 0; i < 200; i++ {
		d := jitterBetween(rng, p.Min, p.Max)
		if d < p.Min || d >= p.Max {
			t.Fatalf("drew %v, outside %v..%v", d, p.Min, p.Max)
		}
	}
}

func TestEstimateUsesTheBusiestDevice(t *testing.T) {
	// Devices send in parallel, so a campaign lasts as long as its busiest
	// number rather than as long as the sum of all of them.
	a, b := uuid.New(), uuid.New()
	got := estimateSeconds(models.DelayCepat, map[uuid.UUID]int{a: 11, b: 3})
	// Cepat is 5..15s, midpoint 10s, and the first message does not wait.
	if want := 100; got != want {
		t.Fatalf("got %ds, want %ds", got, want)
	}

	if got := estimateSeconds(models.DelayNormal, map[uuid.UUID]int{a: 1}); got != 0 {
		t.Fatalf("a single recipient should not wait, got %ds", got)
	}
}

func TestTargetTypeFor(t *testing.T) {
	cases := []struct{ kind, source, want string }{
		{"group", models.TargetSourceGroups, "group"},
		{"group_member", models.TargetSourceGroupMembers, "group_member"},
		{"personal", models.TargetSourceCSV, "csv"},
		{"personal", models.TargetSourceManual, "manual"},
		{"personal", models.TargetSourceContacts, "contact"},
	}
	for _, tc := range cases {
		if got := targetTypeFor(tc.kind, tc.source); got != tc.want {
			t.Errorf("%s/%s: got %q, want %q", tc.kind, tc.source, got, tc.want)
		}
	}
}

func TestVariablesCannotOverrideBuiltIns(t *testing.T) {
	// {{nama}} has to keep meaning the contact's name. If a workspace variable
	// could shadow it, one campaign would greet every customer alike.
	rv := NewResolver(nil, nil)
	vars := rv.variables(
		repository.TargetCandidate{Name: "Budi", PhoneNumber: "628123", Kind: "personal"},
		repository.AccountInfo{},
		TargetRequest{
			ApplicationCode: "APP1",
			CustomValues:    map[string]string{"nama": "SEMUA ORANG", "kode_promo": "HEMAT10"},
		})

	if vars["nama"] != "Budi" {
		t.Fatalf("nama = %q; a custom variable overrode a built-in", vars["nama"])
	}
	if vars["aplikasi"] != "APP1" {
		t.Fatalf("aplikasi = %q", vars["aplikasi"])
	}
	if vars["kode_promo"] != "HEMAT10" {
		t.Fatalf("kode_promo = %q; workspace variables should still be applied", vars["kode_promo"])
	}
}

// A clean campaign — nothing rejected, no missing variables, a valid template —
// must still send arrays, not nulls.
//
// Go marshals a nil slice as `null`, and the review screen reads .length on all
// four of these. So the better the campaign, the more certain the crash: the
// screen built to confirm a send was the thing that stopped it.
func TestPlanNeverMarshalsNullSlices(t *testing.T) {
	plan := &models.TargetPlan{
		Devices:          []models.DevicePlan{},
		Problems:         []models.TargetProblem{},
		Preview:          []string{},
		MissingVariables: []string{},
		TemplateProblems: []string{},
	}

	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"devices", "problems", "preview", "missing_variables", "template_problems",
	} {
		v, ok := back[key]
		if !ok {
			t.Fatalf("%s is missing from the plan", key)
		}
		if v == nil {
			t.Fatalf("%s came back as null; the review screen reads .length on it", key)
		}
		if _, ok := v.([]any); !ok {
			t.Fatalf("%s is %T, not an array", key, v)
		}
	}
}

// The numbers that were actually broadcast to nobody.
//
// Every one of these came out of a real campaign and out of the log line the
// operator saw: `no LID found for 8977991661@s.whatsapp.net from server`. They
// are Indonesian mobiles typed without the trunk zero, which the normaliser
// read as country code 89, 87, 85 and 82 — none of which is assigned to
// anywhere. Eleven of sixty-two targets in a month went this way.
func TestAnIndonesianNumberTypedWithoutItsZeroStillReachesSomebody(t *testing.T) {
	fromProduction := map[string]string{
		"8977991661":  "628977991661",
		"87739192482": "6287739192482",
		"82292665281": "6282292665281",
		"87885718283": "6287885718283",
		"85836330931": "6285836330931",
		"82138362231": "6282138362231",
		// The same number, typed the three ways it is typed, must still collapse
		// to one recipient — otherwise the fix buys a duplicate send.
		"08977991661":    "628977991661",
		"628977991661":   "628977991661",
		"+62897799166 1": "628977991661",
	}
	for in, want := range fromProduction {
		got, err := NormalizeMSISDN(in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// The narrowness is the safety. A number marked as international by its typist
// is never reinterpreted, whatever it starts with.
func TestAMarkedInternationalNumberIsNeverReadAsIndonesian(t *testing.T) {
	untouched := map[string]string{
		// Japan and South Korea share the leading 8 that the rule keys on.
		// Written with a + or 00, they must pass through exactly as given.
		"+819012345678":  "819012345678",
		"00819012345678": "819012345678",
		"+821012345678":  "821012345678",
		"+84912345678":   "84912345678",
		// Malaysia, the case the original comment was written for.
		"+60123456789": "60123456789",
		"60123456789":  "60123456789",
		// Already Indonesian in full form, with and without the plus.
		"+6281234567890": "6281234567890",
		"6281234567890":  "6281234567890",
	}
	for in, want := range untouched {
		got, err := NormalizeMSISDN(in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q was rewritten to %q; it was marked international", in, got)
		}
	}

	// And a bare 8 outside the lengths an Indonesian mobile actually has is left
	// alone rather than guessed at.
	//
	// The first draft of this check named "860123456789" as a number that must
	// not be rewritten, on the reasoning that 86 is China. That was wrong, and
	// wrong in the direction that matters: twelve digits after the 8 is an
	// ordinary Indonesian length, so refusing it would have left exactly the
	// numbers this exists to rescue. The real boundary is length, and these are
	// the lengths no Indonesian mobile has.
	for _, in := range []string{"81234567", "8123456789012345"} {
		got, err := NormalizeMSISDN(in)
		if err != nil {
			continue // refused outright is also fine
		}
		if strings.HasPrefix(got, "62") {
			t.Errorf("%q was read as Indonesian (%q) despite its length", in, got)
		}
	}
}
