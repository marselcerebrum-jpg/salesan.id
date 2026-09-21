package repository

// Resolving who a group participant actually is.
//
// WhatsApp stopped addressing group participants by phone number. Every
// participant row in this workspace is "<lid>@lid", an opaque per-account
// identifier that is neither a name nor a number, and the two things it was
// being used for — falling back to `split_part(jid, '@', 1)` for a number, and
// joining the contact book on `contacts.jid` — both fail on it. The first puts
// a 15-digit LID in a column labelled "nomor"; the second finds no contact at
// all, so the name falls through to that same LID.
//
// The address book already stores both forms (migration 0037 added
// contacts.lid_jid), and whatsmeow keeps its own LID-to-number map. Together
// they resolve a participant to the contact we really have. What neither can
// resolve stays empty: an unknown person is unknown, and printing their LID
// does not make them less so.

// memberContactJoin attaches the address book and the LID map to a query over
// conversation_members aliased `mem`, with its conversation aliased `c`.
//
// The contact side is a lateral aggregate rather than a plain join because a
// person reached through four of our numbers has four contact rows, and a plain
// join would return the member four times. Aggregating also means a name known
// to one of our phones and a number known to another combine into one answer
// instead of competing.
//
// Scoped to the workspace, not to c.account_id: the row is already workspace
// -scoped by its caller, and which of our phones happens to have saved somebody
// is not a fact about who they are.
const memberContactJoin = `
	  left join lateral (
	    select max(nullif(btrim(x.name), ''))          as name,
	           max(nullif(btrim(x.push_name), ''))     as push_name,
	           max(nullif(btrim(x.business_name), '')) as business_name,
	           max(nullif(btrim(x.phone_number), ''))  as phone_number
	      from public.contacts x
	     where x.workspace_id = c.workspace_id
	       and (x.jid = mem.jid or x.lid_jid = mem.jid)
	  ) ct on true
	  -- Matched by splitting the member's JID rather than by concatenating the
	  -- map's. Concatenation wraps the indexed column, so the planner has to read
	  -- the whole map once per member row; splitting leaves lm.lid bare and its
	  -- primary key drives the lookup. Identical results, and the difference only
	  -- shows up once the map is large — which is exactly when it would hurt.
	  left join whatsmeow_lid_map lm
	         on mem.jid like '%@lid'
	        and lm.lid = split_part(mem.jid, '@', 1)`

// memberPhoneExpr is the participant's real phone number, or null.
//
// WhatsApp's own answer comes first: it reports each participant's number
// alongside their LID, and for somebody who has never been a contact and never
// sent us a message that is the only place a number exists at all.
//
// Deliberately null rather than the JID's local part when nothing is known. A
// LID is not a number anyone can dial, message or paste into a spreadsheet, and
// a column of them that looks like numbers is worse than an empty column,
// because only the empty one tells you it is empty.
const memberPhoneExpr = `
	coalesce(
	  nullif(btrim(mem.phone_number), ''),
	  ct.phone_number,
	  nullif(btrim(lm.pn), ''),
	  case when mem.jid like '%@s.whatsapp.net'
	       then split_part(mem.jid, '@', 1) end
	)`

// memberNameExpr is the participant's name as the address book knows them, or
// null when nobody has saved them.
//
// conversation_members.display_name is consulted only when it is not one of
// WhatsApp's masked placeholders ("+62∙∙∙∙∙∙∙∙∙93"), which is what the server
// sends for a participant whose name it will not disclose. Every masked value
// in this workspace is exactly that, and it belongs in neither column: it is
// not a name, and the digits it does show are not a number.
const memberNameExpr = `
	coalesce(
	  ct.name,
	  ct.push_name,
	  ct.business_name,
	  case when btrim(coalesce(mem.display_name, '')) <> ''
	        and mem.display_name !~ '[∙•·]'
	       then btrim(mem.display_name) end
	)`

// The masked placeholder is dropped rather than kept as a hint. It shows a few
// real digits, which is exactly what makes it dangerous: it reads as a number in
// a column of numbers, and there is no way to check the hidden middle against
// anything. A participant we cannot resolve gets an empty number, and the empty
// cell is the true statement.
