package repository

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// Re-opening a campaign in the composer.
//
// The composer writes a campaign out of a form; this reads one back into the
// same form. It exists because the two things an operator wants to do with a
// campaign they already made — send it again with one word changed, or fix it
// before it goes — were both impossible: the pages that showed a campaign were
// reports, and the page that could edit one only ever started empty.
//
// Everything here is read from the rows the campaign already has. Nothing is
// stored twice for the composer's benefit: the recipients come back from the
// resolved target rows, the numbers from the sender-device rows, and the custom
// values from the variables that were rendered onto the targets.

// CampaignSource is one campaign in the shape the composer takes.
type CampaignSource struct {
	CampaignType string `json:"campaign_type"`
	Status       string `json:"status"`
	// Editable is false once a campaign has started. Its content can still be
	// copied into a new one; it can no longer be rewritten, because what it said
	// is what people received.
	Editable bool `json:"editable"`

	Name          string      `json:"name"`
	Body          string      `json:"body"`
	ComposeMode   string      `json:"compose_mode"`
	ApplicationID *uuid.UUID  `json:"application_id"`
	AccountIDs    []uuid.UUID `json:"account_ids"`

	MediaURL         *string `json:"media_url"`
	MediaKind        *string `json:"media_kind"`
	MediaMime        *string `json:"media_mime"`
	MediaSizeBytes   *int64  `json:"media_size_bytes"`
	MediaStoragePath *string `json:"media_storage_path"`
	MediaFileName    *string `json:"media_file_name"`
	Caption          *string `json:"caption"`

	DelayProfile          string `json:"delay_profile"`
	DelayMinSeconds       *int   `json:"delay_min_seconds"`
	DelayMaxSeconds       *int   `json:"delay_max_seconds"`
	AutoRetryOnDisconnect bool   `json:"auto_retry_on_disconnect"`

	Recurrence        string `json:"recurrence"`
	RecurrenceTime    string `json:"recurrence_time"`
	RecurrenceWeekday *int   `json:"recurrence_weekday"`
	RecurrenceDay     *int   `json:"recurrence_day"`

	TargetSource string      `json:"target_source"`
	Numbers      []string    `json:"numbers"`
	ContactIDs   []uuid.UUID `json:"contact_ids"`
	GroupIDs     []uuid.UUID `json:"group_ids"`

	CustomValues map[string]string `json:"custom_values"`
	LabelIDs     []uuid.UUID       `json:"label_ids"`
}

// CampaignSourceFor reads one campaign back into composer shape.
func (r *Repo) CampaignSourceFor(
	ctx context.Context, workspaceID, campaignID uuid.UUID,
) (*CampaignSource, error) {
	var s CampaignSource
	// Empty rather than nil throughout: the composer reads .length and .map on
	// every one of these, and a nil Go slice reaches the browser as null.
	s.AccountIDs = []uuid.UUID{}
	s.Numbers = []string{}
	s.ContactIDs = []uuid.UUID{}
	s.GroupIDs = []uuid.UUID{}
	s.CustomValues = map[string]string{}
	s.LabelIDs = []uuid.UUID{}

	if err := r.pool.QueryRow(ctx, `
		select cc.campaign_type::text, cc.status::text, cc.name,
		       coalesce(cc.message_template, cc.body, ''),
		       coalesce(cc.compose_mode, 'plain'),
		       cc.application_id,
		       cc.media_url, cc.media_kind, cc.media_mime, cc.media_size_bytes,
		       cc.media_storage_path, cc.media_file_name, cc.caption,
		       coalesce(cc.delay_profile, 'normal'),
		       cc.delay_min_seconds, cc.delay_max_seconds,
		       coalesce(cc.auto_retry_on_disconnect, true),
		       coalesce(cc.recurrence, ''),
		       -- Stored as a bare wall clock. Handed back in the same "HH:MM" the
		       -- composer shows, never as a timestamp somebody has to convert.
		       coalesce(to_char(cc.recurrence_time, 'HH24:MI'), ''),
		       cc.recurrence_weekday, cc.recurrence_day,
		       coalesce(cc.target_source, ''),
		       (cc.started_at is null and cc.executed_at is null
		        and cc.status in ('draft', 'scheduled'))
		  from public.content_campaigns cc
		 where cc.id = $1 and cc.workspace_id = $2`, campaignID, workspaceID).
		Scan(&s.CampaignType, &s.Status, &s.Name, &s.Body, &s.ComposeMode,
			&s.ApplicationID, &s.MediaURL, &s.MediaKind, &s.MediaMime, &s.MediaSizeBytes,
			&s.MediaStoragePath, &s.MediaFileName, &s.Caption, &s.DelayProfile,
			&s.DelayMinSeconds, &s.DelayMaxSeconds, &s.AutoRetryOnDisconnect,
			&s.Recurrence, &s.RecurrenceTime, &s.RecurrenceWeekday, &s.RecurrenceDay,
			&s.TargetSource, &s.Editable); err != nil {
		return nil, mapErr(err)
	}

	// The sending numbers, in the order they were chosen.
	rows, err := r.pool.Query(ctx, `
		select account_id from public.broadcast_sender_devices
		 where campaign_id = $1 order by position`, campaignID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		s.AccountIDs = append(s.AccountIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.readCampaignLabelIDs(ctx, campaignID, &s); err != nil {
		return nil, err
	}

	// A Story has no recipient list: WhatsApp decides its audience from the
	// account's own privacy settings.
	if s.CampaignType == "story" {
		return &s, nil
	}
	if err := r.readCampaignAudience(ctx, workspaceID, campaignID, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repo) readCampaignLabelIDs(
	ctx context.Context, campaignID uuid.UUID, s *CampaignSource,
) error {
	rows, err := r.pool.Query(ctx,
		`select label_id from public.campaign_label_assignments where campaign_id = $1`,
		campaignID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		s.LabelIDs = append(s.LabelIDs, id)
	}
	return rows.Err()
}

// readCampaignAudience rebuilds the recipient step from the resolved targets.
//
// The composer's four sources leave different traces, and each is read back on
// its own terms rather than everything being flattened into a list of numbers:
// "kontak" comes back as the same contacts, "grup" as the same groups, and the
// pasted or imported lists as the numbers themselves. A campaign aimed at a
// group therefore re-opens aimed at that group, and picks up whoever joined it
// since — which is what choosing a group meant in the first place.
func (r *Repo) readCampaignAudience(
	ctx context.Context, workspaceID, campaignID uuid.UUID, s *CampaignSource,
) error {
	switch s.TargetSource {
	case "contacts":
		rows, err := r.pool.Query(ctx, `
			select distinct contact_id from public.campaign_targets
			 where campaign_id = $1 and contact_id is not null`, campaignID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			s.ContactIDs = append(s.ContactIDs, id)
		}
		return rows.Err()

	case "groups":
		rows, err := r.pool.Query(ctx, `
			select distinct conversation_id from public.campaign_targets
			 where campaign_id = $1 and conversation_id is not null
			   and target_type = 'group'`, campaignID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			s.GroupIDs = append(s.GroupIDs, id)
		}
		return rows.Err()
	}

	// manual, csv, group_members, and anything older that never recorded a
	// source: the numbers that were actually resolved.
	rows, err := r.pool.Query(ctx, `
		select phone_number from public.campaign_targets
		 where campaign_id = $1 and phone_number is not null and phone_number <> ''
		 order by phone_number`, campaignID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		s.Numbers = append(s.Numbers, n)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// The custom values, recovered from one target's rendered variables.
	//
	// They were applied to every target identically, so any row answers for all
	// of them. Only keys the workspace actually declares as custom variables are
	// kept: the rest of that map is the built-ins (name, number, application),
	// which belong to the recipient and not to the campaign.
	var raw []byte
	err = r.pool.QueryRow(ctx, `
		select t.variables
		  from public.campaign_targets t
		 where t.campaign_id = $1 and t.variables is not null
		 limit 1`, campaignID).Scan(&raw)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	var all map[string]string
	if err := json.Unmarshal(raw, &all); err != nil || len(all) == 0 {
		return nil // an older shape; not worth failing the whole read over
	}

	keys, err := r.pool.Query(ctx,
		`select key from public.custom_variables where workspace_id = $1`, workspaceID)
	if err != nil {
		return err
	}
	defer keys.Close()
	for keys.Next() {
		var k string
		if err := keys.Scan(&k); err != nil {
			return err
		}
		if v, ok := all[k]; ok {
			s.CustomValues[k] = v
		}
	}
	return keys.Err()
}
