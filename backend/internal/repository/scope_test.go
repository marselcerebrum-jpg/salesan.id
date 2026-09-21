package repository

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Access rules, tested as the pure predicates they are.
//
// These decide what one person may read about another's work, so they are worth
// testing without a database in the way: the interesting cases are the ones
// where a role is missing, an application list is empty, or somebody asks about
// themselves.

func TestLeaderSeesEverything(t *testing.T) {
	sc := Scope{Role: models.RoleLeader, All: true, UserID: uuid.New()}

	if !sc.CanSeeApplication(uuid.New()) {
		t.Error("a Leader must see every application, including ones added later")
	}
	if !sc.CanSeeAdmin(uuid.New()) {
		t.Error("a Leader must see every admin")
	}
	if !sc.CanManageSchedules() {
		t.Error("a Leader must be able to manage schedules")
	}
}

func TestPICSeesOnlyTheirApplicationsAndTeam(t *testing.T) {
	mine, theirs := uuid.New(), uuid.New()
	me, freelance, stranger := uuid.New(), uuid.New(), uuid.New()

	sc := Scope{
		Role:           models.RolePIC,
		UserID:         me,
		ApplicationIDs: []uuid.UUID{mine},
		AdminIDs:       []uuid.UUID{me, freelance},
	}

	if !sc.CanSeeApplication(mine) {
		t.Error("a PIC must see the application they hold")
	}
	if sc.CanSeeApplication(theirs) {
		t.Error("a PIC must not see another PIC's application")
	}
	if !sc.CanSeeAdmin(freelance) {
		t.Error("a PIC must see the Freelance under them")
	}
	if sc.CanSeeAdmin(stranger) {
		t.Error("a PIC must not see somebody else's Freelance")
	}
	if !sc.CanManageSchedules() {
		t.Error("a PIC schedules their own team")
	}
}

func TestFreelanceSeesOnlyThemselves(t *testing.T) {
	me, colleague := uuid.New(), uuid.New()
	app := uuid.New()

	sc := Scope{
		Role:           models.RoleFreelance,
		UserID:         me,
		ApplicationIDs: []uuid.UUID{app},
		AdminIDs:       []uuid.UUID{me},
	}

	if !sc.CanSeeAdmin(me) {
		t.Error("everybody sees their own figures")
	}
	if sc.CanSeeAdmin(colleague) {
		t.Error("a Freelance must never see another Freelance's figures")
	}
	if !sc.CanSeeApplication(app) {
		t.Error("a Freelance sees the application assigned to them")
	}
	if sc.CanSeeApplication(uuid.New()) {
		t.Error("a Freelance must not see an application they are not on")
	}
	if sc.CanManageSchedules() {
		t.Error("a Freelance does not write their own rota")
	}
}

// Who may hire whom. A PIC running their own team is the point of the
// hierarchy; a PIC quietly creating another Leader would defeat it.
func TestCreatableRoles(t *testing.T) {
	leader := Scope{Role: models.RoleLeader, All: true, UserID: uuid.New()}
	if got := leader.CreatableRoles(); len(got) != 3 {
		t.Errorf("a Leader hires anybody, got %v", got)
	}

	pic := Scope{Role: models.RolePIC, UserID: uuid.New()}
	if got := pic.CreatableRoles(); len(got) != 1 || got[0] != models.RoleFreelance {
		t.Errorf("a PIC hires only Freelance, got %v", got)
	}
	if pic.CanCreateRole(models.RoleLeader) || pic.CanCreateRole(models.RolePIC) {
		t.Error("a PIC must not be able to create a Leader or another PIC")
	}
	if !pic.CanCreateRole(models.RoleFreelance) {
		t.Error("a PIC must be able to create a Freelance")
	}

	freelance := Scope{Role: models.RoleFreelance, UserID: uuid.New()}
	if len(freelance.CreatableRoles()) != 0 {
		t.Error("a Freelance hires nobody")
	}
	// Parenthesised: Go cannot parse a bare composite literal in a condition.
	if (Scope{}).CanCreateRole(models.RoleFreelance) {
		t.Error("somebody with no role must not create accounts")
	}
}

// A PIC manages their own team and nothing else — including not themselves,
// because narrowing your own scope is the one mistake you cannot undo alone.
func TestPICManagesOnlyTheirOwnTeam(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	mine, theirs := uuid.New(), uuid.New()

	pic := Scope{Role: models.RolePIC, UserID: me}

	if !pic.CanManageMember(mine, models.RoleFreelance, &me) {
		t.Error("a PIC manages the Freelance reporting to them")
	}
	if pic.CanManageMember(theirs, models.RoleFreelance, &other) {
		t.Error("a PIC must not manage another PIC's Freelance")
	}
	if pic.CanManageMember(mine, models.RoleFreelance, nil) {
		t.Error("a Freelance with no PIC is not automatically theirs")
	}
	if pic.CanManageMember(other, models.RolePIC, nil) {
		t.Error("a PIC must not manage another PIC")
	}
	if pic.CanManageMember(me, models.RolePIC, nil) {
		t.Error("a PIC must not manage themselves")
	}

	leader := Scope{Role: models.RoleLeader, All: true, UserID: me}
	if !leader.CanManageMember(theirs, models.RoleFreelance, &other) {
		t.Error("a Leader manages anybody")
	}
}

// Unattributed activity — a message typed on the phone — has no admin. A
// Leader and the PIC who owns the application may see it; a Freelance may not,
// because it is neither their work nor anybody's verifiable work.
func TestUnattributedRowsAreVisibleToLeadersAndPICsOnly(t *testing.T) {
	me := uuid.New()

	leader := Scope{Role: models.RoleLeader, All: true, UserID: me}
	if got := adminVisible("m.sent_by", leader, &queryArgs{}); got != "" {
		t.Errorf("a Leader needs no admin restriction at all, got %q", got)
	}

	pic := Scope{Role: models.RolePIC, UserID: me, AdminIDs: []uuid.UUID{me}}
	if got := adminVisible("m.sent_by", pic, &queryArgs{}); !strings.Contains(got, "is null") {
		t.Errorf("a PIC must still see unattributed rows, got %q", got)
	}

	freelance := Scope{Role: models.RoleFreelance, UserID: me, AdminIDs: []uuid.UUID{me}}
	got := adminVisible("m.sent_by", freelance, &queryArgs{})
	if strings.Contains(got, "is null") {
		t.Errorf("a Freelance must not see unattributed rows, got %q", got)
	}
	if !strings.Contains(got, "m.sent_by = $1") {
		t.Errorf("a Freelance must be pinned to their own id, got %q", got)
	}
}

// A PIC's team is themselves plus everyone reporting to them, which is what
// makes their team total include their own work rather than only their staff's.
func TestPICTeamIncludesThemselves(t *testing.T) {
	pic, freelance := uuid.New(), uuid.New()
	members := []models.MemberPerformance{
		{UserID: &pic, Role: models.RolePIC},
		{UserID: &freelance, Role: models.RoleFreelance, PICUserID: &pic},
	}

	teamOf := map[uuid.UUID]uuid.UUID{}
	for _, m := range members {
		key := *m.UserID
		if m.PICUserID != nil {
			key = *m.PICUserID
		}
		teamOf[*m.UserID] = key
	}

	if teamOf[pic] != pic {
		t.Error("a PIC counts towards their own team")
	}
	if teamOf[freelance] != pic {
		t.Error("a Freelance counts towards their PIC's team")
	}
}

// The dangerous default. Somebody with no role and no assignments must resolve
// to an empty scope, not to an unrestricted one — an empty application list has
// to mean "nothing", never "no filter".
func TestUnassignedMemberSeesNothingButThemselves(t *testing.T) {
	me := uuid.New()
	sc := Scope{UserID: me, AdminIDs: []uuid.UUID{me}}

	if sc.IsLeader() {
		t.Fatal("a member with no role is not a Leader")
	}
	if sc.CanSeeApplication(uuid.New()) {
		t.Error("an empty application list must grant nothing")
	}
	if sc.CanSeeAdmin(uuid.New()) {
		t.Error("an unassigned member must not see anyone else")
	}
	if !sc.CanSeeAdmin(me) {
		t.Error("they still see themselves")
	}
	if sc.CanManageSchedules() {
		t.Error("an unassigned member must not manage schedules")
	}
}
