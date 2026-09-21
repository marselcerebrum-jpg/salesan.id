package wa

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Reading channel posts without losing the parts whatsmeow drops.
//
// whatsmeow's GetNewsletterMessages keeps three children of each post — the
// payload, the view count and the reactions — and silently discards the rest.
// A channel poll's vote tallies arrive as one of the discarded children, which
// is why the panel could show a poll but never its votes. So the same request is
// made here and every child is kept.

// rawChannelPost is one post with everything the server sent about it.
type rawChannelPost struct {
	ServerID  types.MessageServerID
	Timestamp int64
	Message   *waE2E.Message
	Views     int
	Reactions map[string]int
	// Tallies is what the server reported against each option, keyed however
	// the server keyed it (see matchTally).
	Tallies map[string]int
	// Raw is the post's node, for the debug dump when a poll's tallies could not
	// be recognised.
	Raw *waBinary.Node
}

// fetchChannelPosts asks for a channel's recent posts ("messages") or for the
// counter updates on them ("message_updates"). Same request whatsmeow makes.
func fetchChannelPosts(
	ctx context.Context, s *Session, target types.JID, tag string, count int,
) ([]rawChannelPost, error) {
	attrs := waBinary.Attrs{"count": count}
	if tag == "messages" {
		attrs["type"] = "jid"
		attrs["jid"] = target
	}
	query := whatsmeow.DangerousInfoQuery{
		Namespace: "newsletter",
		Type:      whatsmeow.DangerousInfoQueryType("get"),
		To:        types.ServerJID,
		Content:   []waBinary.Node{{Tag: tag, Attrs: attrs}},
	}
	if tag == "message_updates" {
		query.To = target
	}
	resp, err := s.client.DangerousInternals().SendIQ(ctx, query)
	if err != nil {
		return nil, err
	}
	list, ok := resp.GetOptionalChildByTag(tag)
	if !ok {
		return nil, fmt.Errorf("respons saluran tanpa <%s>", tag)
	}
	// Updates arrive one level deeper: <message_updates><messages><message>.
	// Reading <message> straight under <message_updates> found nothing, which
	// is why views and votes from the phone never reached the web.
	if inner, ok := list.GetOptionalChildByTag("messages"); ok {
		list = inner
	}

	out := []rawChannelPost{}
	for _, child := range list.GetChildren() {
		if child.Tag != "message" {
			continue
		}
		node := child
		ag := node.AttrGetter()
		p := rawChannelPost{
			ServerID:  types.MessageServerID(ag.Int("server_id")),
			Timestamp: ag.OptionalUnixTime("t").Unix(),
			Reactions: map[string]int{},
			Tallies:   map[string]int{},
			Raw:       &node,
		}
		for _, sub := range node.GetChildren() {
			switch sub.Tag {
			case "plaintext":
				if b, ok := sub.Content.([]byte); ok {
					m := new(waE2E.Message)
					if proto.Unmarshal(b, m) == nil {
						p.Message = m
					}
				}
			case "views_count":
				p.Views = sub.AttrGetter().OptionalInt("count")
			case "reactions":
				for _, r := range sub.GetChildren() {
					rag := r.AttrGetter()
					p.Reactions[rag.OptionalString("code")] = rag.OptionalInt("count")
				}
			default:
				collectTallies(sub, p.Tallies)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// collectTallies walks one unknown child and records every descendant that
// carries a count, under whatever names it with.
func collectTallies(n waBinary.Node, into map[string]int) {
	for _, c := range n.GetChildren() {
		if count, ok := c.Attrs["count"]; ok {
			for _, attr := range []string{"hash", "option_hash", "option", "name", "vote", "id", "idx", "index"} {
				if v, ok := c.Attrs[attr]; ok {
					into[fmt.Sprint(v)] = toInt(count)
					break
				}
			}
			// A key sent as the node's content rather than as an attribute.
			if b, ok := c.Content.([]byte); ok && len(b) > 0 {
				into[hex.EncodeToString(b)] = toInt(count)
			}
		}
		collectTallies(c, into)
	}
}

func toInt(v any) int {
	var n int
	_, _ = fmt.Sscan(fmt.Sprint(v), &n)
	return n
}

// matchTally finds the count reported for one option, whatever form the key
// took: the option's SHA-256 (hex, or base64 either way), its text, or its
// position.
func matchTally(tallies map[string]int, name string, index int) (int, bool) {
	if len(tallies) == 0 {
		return 0, false
	}
	sum := sha256.Sum256([]byte(name))
	for _, key := range []string{
		hex.EncodeToString(sum[:]),
		strings.ToUpper(hex.EncodeToString(sum[:])),
		base64.StdEncoding.EncodeToString(sum[:]),
		base64.RawStdEncoding.EncodeToString(sum[:]),
		base64.URLEncoding.EncodeToString(sum[:]),
		base64.RawURLEncoding.EncodeToString(sum[:]),
		name,
		fmt.Sprint(index),
	} {
		if n, ok := tallies[key]; ok {
			return n, true
		}
	}
	return 0, false
}

// dumpUnrecognisedPoll writes the structure of one poll post to
// logs/channel-poll-debug.xml when its tallies could not be matched to options.
//
// Diagnostic, and deliberately narrow: only poll posts, only when matching
// failed, overwritten each time rather than growing. A channel post is public
// content — nothing here is a credential — but the payload bytes are dropped
// anyway, because the structure is the only thing being looked for.
func dumpUnrecognisedPoll(p rawChannelPost, update *rawChannelPost) {
	if p.Raw == nil {
		return
	}
	out := "<!-- messages -->\n" + stripPayload(*p.Raw).String() + "\n"
	if update != nil && update.Raw != nil {
		out += "<!-- message_updates -->\n" + stripPayload(*update.Raw).String() + "\n"
	} else {
		out += "<!-- message_updates: tidak ada entri untuk postingan ini -->\n"
	}
	_ = os.MkdirAll("logs", 0o755)
	_ = os.WriteFile(filepath.Join("logs", "channel-poll-debug.xml"), []byte(out), 0o644)
}

// dumpUpdates writes every entry of one message_updates reply, structure only,
// to logs/channel-updates-debug.xml. Same scope as dumpUnrecognisedPoll: only
// written while a poll's tallies are unreadable.
func dumpUpdates(list []rawChannelPost) {
	out := fmt.Sprintf("<!-- message_updates: %d entri -->\n", len(list))
	for _, u := range list {
		if u.Raw != nil {
			out += stripPayload(*u.Raw).String() + "\n"
		}
	}
	_ = os.MkdirAll("logs", 0o755)
	_ = os.WriteFile(filepath.Join("logs", "channel-updates-debug.xml"), []byte(out), 0o644)
}

// stripPayload replaces the post's own bytes, keeping only the structure.
func stripPayload(n waBinary.Node) waBinary.Node {
	children := []waBinary.Node{}
	for _, c := range n.GetChildren() {
		if c.Tag == "plaintext" {
			c = waBinary.Node{Tag: "plaintext", Attrs: c.Attrs, Content: "(dihapus)"}
		}
		children = append(children, c)
	}
	n.Content = children
	return n
}
