package repository

import (
	"context"
	"testing"
	"time"


	"github.com/salesan/omnichannel/backend/internal/models"
)

// Integration tests for group membership tracking.
//
// The rules that matter here live in SQL — a unique index that collapses the
// same notification heard by several of our numbers, and a guard that refuses to
// anchor a group nobody has fetched. Neither can be vouched for by a fake.
//
//	$env:SALESAN_TEST_DATABASE_URL = "postgres://..."   # a scratch database
//	go test ./internal/repository -run IntegrationGroupMember -v

func groupFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t, models.ConversationTypeGroup)
	var jid string
	if err := f.repo.pool.QueryRow(context.Background(),
		`select chat_jid from public.conversations where id = $1`, f.conversationID).Scan(&jid); err != nil {
		t.Fatalf("read chat jid: %v", err)
	}
	return f, jid
}

// seedMembers puts a group's member list in place, the way a fetch would.
func (f *fixture) seedMembers(t *testing.T, jids ...string) {
	t.Helper()
	for _, j := range jids {
		if _, err := f.repo.pool.Exec(context.Background(),
			`insert into public.conversation_members (conversation_id, jid) values ($1, $2)
			 on conflict do nothing`, f.conversationID, j); err != nil {
			t.Fatalf("seed member %s: %v", j, err)
		}
	}
}

// The mistake this whole design is arranged to avoid. One person joins one
// group; five of our numbers are in that group and all five are told about it.
// The group grew by one.
func TestIntegrationGroupMemberEventCountedOncePerGroup(t *testing.T) {
	f, chatJID := groupFixture(t)
	ctx := context.Background()
	f.seedMembers(t, "628000000001@s.whatsapp.net")

	at := time.Now().UTC()
	change := []GroupMemberChange{{ParticipantJID: "628999000111@s.whatsapp.net"}}

	joined, left, err := f.repo.RecordGroupMemberChanges(
		ctx, f.workspaceID, chatJID, f.accountID, "", at, change)
	if err != nil {
		t.Fatal(err)
	}
	if joined != 1 || left != 0 {
		t.Fatalf("first report: joined=%d left=%d, want 1 and 0", joined, left)
	}

	// The same notification, heard by four more of our numbers a few
	// milliseconds apart, as it actually arrives.
	for i := 0; i < 4; i++ {
		again, _, err := f.repo.RecordGroupMemberChanges(
			ctx, f.workspaceID, chatJID, f.accountID, "",
			at.Add(time.Duration(i+1)*20*time.Millisecond), change)
		if err != nil {
			t.Fatal(err)
		}
		if again != 0 {
			t.Errorf("number %d reported %d new arrivals; the first one already did", i+2, again)
		}
	}

	deltas, err := f.repo.GroupMemberDeltaToday(ctx, f.workspaceID, []string{chatJID})
	if err != nil {
		t.Fatal(err)
	}
	if deltas[chatJID] != 1 {
		t.Errorf("today's change = %d, want 1: one person joined, five phones saw it",
			deltas[chatJID])
	}
}

// A group that churns is not a group that grew. The badge shows the net, and
// the net of one in and one out is nothing.
func TestIntegrationGroupMemberDeltaIsNet(t *testing.T) {
	f, chatJID := groupFixture(t)
	ctx := context.Background()
	f.seedMembers(t, "628000000001@s.whatsapp.net", "628000000002@s.whatsapp.net")

	now := time.Now().UTC()
	if _, _, err := f.repo.RecordGroupMemberChanges(ctx, f.workspaceID, chatJID, f.accountID, "", now,
		[]GroupMemberChange{
			{ParticipantJID: "628999000111@s.whatsapp.net"},
			{ParticipantJID: "628999000222@s.whatsapp.net"},
			{ParticipantJID: "628000000002@s.whatsapp.net", Leaving: true},
		}); err != nil {
		t.Fatal(err)
	}

	deltas, err := f.repo.GroupMemberDeltaToday(ctx, f.workspaceID, []string{chatJID})
	if err != nil {
		t.Fatal(err)
	}
	if deltas[chatJID] != 1 {
		t.Errorf("net change = %d, want 1 (two in, one out)", deltas[chatJID])
	}

	history, _, err := f.repo.GroupMemberHistory(ctx, f.workspaceID, chatJID, jakartaNow().Year(), int(jakartaNow().Month()))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history has %d days, want 1", len(history))
	}
	if history[0].Joined != 2 || history[0].Left != 1 {
		t.Errorf("day = +%d/-%d, want +2/-1", history[0].Joined, history[0].Left)
	}
	if history[0].MemberCount == 0 {
		t.Error("the day was anchored at zero although the group has members")
	}
}

// A group nobody has fetched has an empty member list. Anchoring its history at
// zero would draw a chart claiming it lost everyone it has, so the arrival is
// recorded and the head count waits until there is a real one.
func TestIntegrationGroupMemberSkipsAnchorForUnfetchedGroup(t *testing.T) {
	f, chatJID := groupFixture(t)
	ctx := context.Background()

	if _, _, err := f.repo.RecordGroupMemberChanges(ctx, f.workspaceID, chatJID, f.accountID, "",
		time.Now().UTC(),
		[]GroupMemberChange{{ParticipantJID: "628999000111@s.whatsapp.net"}}); err != nil {
		t.Fatal(err)
	}

	deltas, err := f.repo.GroupMemberDeltaToday(ctx, f.workspaceID, []string{chatJID})
	if err != nil {
		t.Fatal(err)
	}
	if deltas[chatJID] != 1 {
		t.Errorf("the arrival was not recorded: delta = %d, want 1", deltas[chatJID])
	}

	history, _, err := f.repo.GroupMemberHistory(ctx, f.workspaceID, chatJID, jakartaNow().Year(), int(jakartaNow().Month()))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Errorf("an unfetched group was anchored at %d members", history[0].MemberCount)
	}
}

// The same guard on the other write path: one arrival must not turn "nobody has
// looked yet" into "this group has one member".
func TestIntegrationAddGroupMembersLeavesUnfetchedListEmpty(t *testing.T) {
	f, _ := groupFixture(t)
	ctx := context.Background()
	newcomer := []string{"628999000111@s.whatsapp.net"}

	if err := f.repo.AddGroupMembers(ctx, f.conversationID, newcomer); err != nil {
		t.Fatal(err)
	}
	if n := f.memberCount(t); n != 0 {
		t.Fatalf("unfetched group now claims %d members", n)
	}

	// Once the list is real, arrivals join it.
	f.seedMembers(t, "628000000001@s.whatsapp.net")
	if err := f.repo.AddGroupMembers(ctx, f.conversationID, newcomer); err != nil {
		t.Fatal(err)
	}
	if n := f.memberCount(t); n != 2 {
		t.Errorf("member count = %d, want 2", n)
	}

	if err := f.repo.RemoveGroupMembers(ctx, f.conversationID, newcomer); err != nil {
		t.Fatal(err)
	}
	if n := f.memberCount(t); n != 1 {
		t.Errorf("after the departure member count = %d, want 1", n)
	}
}

// The anchor exists so a missed notification spoils one day instead of every
// day after it.
func TestIntegrationGroupMemberSnapshotAnchorsTheDay(t *testing.T) {
	f, chatJID := groupFixture(t)
	ctx := context.Background()
	f.seedMembers(t,
		"628000000001@s.whatsapp.net",
		"628000000002@s.whatsapp.net",
		"628000000003@s.whatsapp.net")

	if _, err := f.repo.SnapshotGroupMemberCounts(ctx); err != nil {
		t.Fatal(err)
	}

	history, _, err := f.repo.GroupMemberHistory(ctx, f.workspaceID, chatJID, jakartaNow().Year(), int(jakartaNow().Month()))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history has %d days, want 1", len(history))
	}
	if history[0].MemberCount != 3 {
		t.Errorf("anchored at %d members, want 3", history[0].MemberCount)
	}
	// The snapshot is a head count, not a tally: it must not invent arrivals.
	if history[0].Joined != 0 || history[0].Left != 0 {
		t.Errorf("snapshot reported +%d/-%d, want nothing",
			history[0].Joined, history[0].Left)
	}
}

func (f *fixture) memberCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.repo.pool.QueryRow(context.Background(),
		`select count(*) from public.conversation_members where conversation_id = $1`,
		f.conversationID).Scan(&n); err != nil {
		t.Fatalf("count members: %v", err)
	}
	return n
}

// jakartaNow is the clock the day boundaries are drawn on, so a test running at
// 23:30 UTC asks for the same month the code under test would have picked.
func jakartaNow() time.Time {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*60*60)
	}
	return time.Now().In(loc)
}
