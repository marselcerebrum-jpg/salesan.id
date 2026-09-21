package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// The API-side half of access control.
//
// The RLS policies say the same things for the browser's direct Supabase path,
// but the backend connects as the service role, for which RLS does not run — so
// a rule that exists only in a policy does not exist on this path at all. These
// are the checks that actually refuse, tested as the pure predicates they are.

func TestEnforceScopePinsFreelanceToThemselves(t *testing.T) {
	me, colleague := uuid.New(), uuid.New()
	app := uuid.New()

	sc := repository.Scope{
		Role:           models.RoleFreelance,
		UserID:         me,
		ApplicationIDs: []uuid.UUID{app},
		AdminIDs:       []uuid.UUID{me},
	}

	// Asking for their own figures is allowed and left alone.
	w := httptest.NewRecorder()
	got, ok := enforceScope(w, sc, models.AnalyticsFilter{AdminID: &me})
	if !ok {
		t.Fatalf("a Freelance must be able to read their own work, got %d", w.Code)
	}
	if got.AdminID == nil || *got.AdminID != me {
		t.Error("their own id must survive")
	}

	// Asking for somebody else's is refused outright rather than quietly
	// narrowed: handing them their own numbers under a colleague's name would
	// be worse than saying no.
	w = httptest.NewRecorder()
	if _, ok := enforceScope(w, sc, models.AnalyticsFilter{AdminID: &colleague}); ok {
		t.Error("a Freelance must not be able to read a colleague's figures")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}

	// Asking for nothing in particular still pins them to themselves. This is
	// the case that matters: without it, every figure whose table has no admin
	// column would fall back to "everything in my application".
	w = httptest.NewRecorder()
	got, ok = enforceScope(w, sc, models.AnalyticsFilter{})
	if !ok {
		t.Fatal("an unfiltered request from a Freelance is legitimate")
	}
	if got.AdminID == nil || *got.AdminID != me {
		t.Fatal("an unfiltered Freelance request must be pinned to their own id")
	}
	if got.PICUserID != nil {
		t.Error("a Freelance has no team to filter by")
	}
}

func TestEnforceScopeRefusesForeignApplication(t *testing.T) {
	mine, theirs := uuid.New(), uuid.New()
	sc := repository.Scope{
		Role:           models.RolePIC,
		UserID:         uuid.New(),
		ApplicationIDs: []uuid.UUID{mine},
	}

	w := httptest.NewRecorder()
	if _, ok := enforceScope(w, sc, models.AnalyticsFilter{ApplicationID: &mine}); !ok {
		t.Error("a PIC must be able to read their own application")
	}

	w = httptest.NewRecorder()
	if _, ok := enforceScope(w, sc, models.AnalyticsFilter{ApplicationID: &theirs}); ok {
		t.Error("a PIC must not be able to read another PIC's application")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

func TestEnforceScopeLeavesLeaderAlone(t *testing.T) {
	sc := repository.Scope{Role: models.RoleLeader, All: true, UserID: uuid.New()}
	anybody, anywhere := uuid.New(), uuid.New()

	w := httptest.NewRecorder()
	got, ok := enforceScope(w, sc, models.AnalyticsFilter{
		AdminID:       &anybody,
		ApplicationID: &anywhere,
	})
	if !ok {
		t.Fatalf("a Leader may read anything, got %d", w.Code)
	}
	if got.AdminID == nil || *got.AdminID != anybody {
		t.Error("a Leader's chosen admin filter must survive")
	}
}

// One campaign, one application.
//
// Enforced in the database too (migration 0031), so this is the readable
// refusal rather than the only one — but it is the one that produces a sentence
// an operator can act on instead of a constraint violation.
func TestStrayDevice(t *testing.T) {
	app, other := uuid.New(), uuid.New()

	same := []repository.AccountInfo{
		{Name: "ASNQ-1", ApplicationID: &app},
		{Name: "ASNQ-2", ApplicationID: &app},
	}
	if got := strayDevice(same, app); got != "" {
		t.Errorf("numbers from one application are fine, got %q", got)
	}

	mixed := []repository.AccountInfo{
		{Name: "ASNQ-1", ApplicationID: &app},
		{Name: "TOEFL-1", ApplicationID: &other},
	}
	if got := strayDevice(mixed, app); got != "TOEFL-1" {
		t.Errorf("got %q, want the offending number named", got)
	}

	// A number belonging to no application is stray as well: it sits outside
	// the hierarchy, so it belongs to no campaign either.
	unassigned := []repository.AccountInfo{{Name: "Belum ditugaskan"}}
	if got := strayDevice(unassigned, app); got != "Belum ditugaskan" {
		t.Errorf("got %q, want an unassigned number refused", got)
	}
}

func TestValidDelayProfileRejectsUnknown(t *testing.T) {
	for _, p := range models.DelayProfileNames() {
		if !validDelayProfile(p) {
			t.Errorf("%s is one of the five profiles", p)
		}
	}
	if validDelayProfile("secepat-kilat") {
		t.Error("an unknown profile must be refused rather than silently defaulted")
	}
}

func TestVocabularyIsDefinedByLeadersAndPICs(t *testing.T) {
	leader := repository.Scope{Role: models.RoleLeader, All: true}
	pic := repository.Scope{Role: models.RolePIC}
	freelance := repository.Scope{Role: models.RoleFreelance}

	if !canDefineVocabulary(leader) || !canDefineVocabulary(pic) {
		t.Error("a Leader and a PIC define campaign labels and variables")
	}
	if canDefineVocabulary(freelance) {
		t.Error("a Freelance uses the vocabulary but does not name it: those names appear in everybody's composer")
	}
}
