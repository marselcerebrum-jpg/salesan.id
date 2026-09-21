package analytics

import (
	"testing"
	"time"
)

func mention(offset time.Duration) Message {
	m := inbound(offset)
	m.MentionsMe = true
	m.ParticipantJID = "628123456789@s.whatsapp.net"
	m.SenderPhone = "628123456789"
	return m
}

func TestMentionPairedWithTheReply(t *testing.T) {
	tagged := mention(0)
	reply := outbound(4*time.Minute, SourceWebAdmin)

	got := ComputeMentions([]Message{tagged, reply})
	if len(got) != 1 {
		t.Fatalf("got %d mentions, want 1", len(got))
	}
	if got[0].ResponseMessageID == nil || *got[0].ResponseMessageID != reply.ID {
		t.Fatal("the reply was not linked to the mention")
	}
	if got[0].ResponderAdminID == nil {
		t.Error("a web reply must carry the admin who sent it")
	}
}

func TestMentionWithoutReplyStaysOpen(t *testing.T) {
	got := ComputeMentions([]Message{mention(0), inbound(time.Minute)})
	if len(got) != 1 {
		t.Fatalf("got %d mentions, want 1", len(got))
	}
	if got[0].RespondedAt != nil {
		t.Error("nobody answered, so it must stay open")
	}
}

// Opening a group is not answering it. A broadcast landing there afterwards is
// not answering it either.
func TestMentionNotAnsweredByAutomation(t *testing.T) {
	got := ComputeMentions([]Message{
		mention(0),
		outbound(time.Minute, SourceBroadcast),
		outbound(2*time.Minute, SourceBot),
	})
	if got[0].RespondedAt != nil {
		t.Error("only a manual reply counts as answering a mention")
	}
}

func TestMentionIgnoresMessagesThatDoNotNameUs(t *testing.T) {
	if got := ComputeMentions([]Message{inbound(0), outbound(time.Minute, SourceWebAdmin)}); len(got) != 0 {
		t.Fatalf("got %d mentions, want none", len(got))
	}
}

func TestMentionIgnoresOurOwnMessages(t *testing.T) {
	own := outbound(0, SourceWebAdmin)
	own.MentionsMe = true
	if got := ComputeMentions([]Message{own}); len(got) != 0 {
		t.Fatal("our own message can never be a mention of us")
	}
}

// An earlier reply must not be credited to a later mention.
func TestMentionUsesTheFirstReplyAfterIt(t *testing.T) {
	first := mention(0)
	early := outbound(1*time.Minute, SourceWebAdmin)
	second := mention(10 * time.Minute)
	late := outbound(12*time.Minute, SourceWebAdmin)

	got := ComputeMentions([]Message{first, early, second, late})
	if len(got) != 2 {
		t.Fatalf("got %d mentions, want 2", len(got))
	}
	if *got[0].ResponseMessageID != early.ID {
		t.Error("first mention linked to the wrong reply")
	}
	if *got[1].ResponseMessageID != late.ID {
		t.Error("second mention linked to the wrong reply")
	}
}
