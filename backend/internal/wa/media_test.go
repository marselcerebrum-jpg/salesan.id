package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

// The guard that keeps other people's Status off the disk is a JID comparison,
// and the first version of it compared the whole struct. A JID carries
// RawAgent, Device and Integrator beside the user and server, so a chat that
// prints as "status@broadcast" can still fail `==` when the wire set one of
// those — which is exactly what happened: the guard shipped, and 59 GB of
// Status video went on being downloaded because its test was never true.
func TestStatusChatIsRecognisedWhateverElseTheJIDCarries(t *testing.T) {
	plain := types.NewJID("status", types.BroadcastServer)
	if !isStatusChat(plain) {
		t.Fatal("status@broadcast must be recognised")
	}

	// The fields that broke it.
	for _, dressed := range []types.JID{
		{User: "status", Server: types.BroadcastServer, RawAgent: 1},
		{User: "status", Server: types.BroadcastServer, Device: 3},
		{User: "status", Server: types.BroadcastServer, Integrator: 7},
	} {
		if !isStatusChat(dressed) {
			t.Errorf("status chat missed when the JID also carried %+v", dressed)
		}
	}

	// A customer's own chat is not Status, and must keep being fetched.
	if isStatusChat(types.NewJID("628123456789", types.DefaultUserServer)) {
		t.Error("an ordinary chat must not be taken for Status")
	}
	// Neither is a broadcast list, which is a different thing on the same server.
	if isStatusChat(types.NewJID("628123456789", types.BroadcastServer)) {
		t.Error("a broadcast list must not be taken for Status")
	}
}
