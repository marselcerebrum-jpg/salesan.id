package repository

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Campaign labels and custom variables.
//
// Both are workspace-level vocabulary rather than per-campaign data, and both
// are deliberately separate from anything WhatsApp knows about. A campaign label
// is our own filing — "Promo Ramadan", "Uji coba" — and never reaches a
// customer's phone; conversation_labels are the ones WhatsApp syncs. Giving them
// one table would eventually send an internal note to somebody's device.

// ListCampaignLabels returns the workspace's campaign labels.
func (r *Repo) ListCampaignLabels(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]models.CampaignLabel, error) {
	q := `
		select l.id, l.name, l.color, l.archived_at,
		       (select count(*) from public.campaign_label_assignments a where a.label_id = l.id),
		       l.created_at
		  from public.campaign_labels l
		 where l.workspace_id = $1`
	if !includeArchived {
		q += " and l.archived_at is null"
	}
	q += " order by l.archived_at nulls first, lower(l.name)"

	rows, err := r.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CampaignLabel{}
	for rows.Next() {
		var l models.CampaignLabel
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.ArchivedAt, &l.CampaignCount, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CampaignLabelsFor lists the labels attached to one campaign.
func (r *Repo) CampaignLabelsFor(ctx context.Context, campaignID uuid.UUID) ([]models.CampaignLabel, error) {
	rows, err := r.pool.Query(ctx, `
		select l.id, l.name, l.color, l.archived_at, 0, l.created_at
		  from public.campaign_label_assignments a
		  join public.campaign_labels l on l.id = a.label_id
		 where a.campaign_id = $1
		 order by lower(l.name)`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CampaignLabel{}
	for rows.Next() {
		var l models.CampaignLabel
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.ArchivedAt, &l.CampaignCount, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateCampaignLabel adds one.
func (r *Repo) CreateCampaignLabel(
	ctx context.Context, workspaceID, createdBy uuid.UUID, name, color string,
) (*models.CampaignLabel, error) {
	if color == "" {
		color = "#0F766E"
	}
	var l models.CampaignLabel
	err := r.pool.QueryRow(ctx, `
		insert into public.campaign_labels (workspace_id, name, color, created_by)
		values ($1, $2, $3, $4)
		returning id, name, color, archived_at, 0, created_at`,
		workspaceID, strings.TrimSpace(name), color, createdBy).
		Scan(&l.ID, &l.Name, &l.Color, &l.ArchivedAt, &l.CampaignCount, &l.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

// UpdateCampaignLabel renames, recolours, archives or restores one.
//
// Archiving rather than deleting: a label that named a campaign last quarter is
// part of that campaign's record, and removing it would quietly change history.
func (r *Repo) UpdateCampaignLabel(
	ctx context.Context, workspaceID, labelID uuid.UUID,
	name, color *string, archived *bool,
) (*models.CampaignLabel, error) {
	var l models.CampaignLabel
	err := r.pool.QueryRow(ctx, `
		update public.campaign_labels
		   set name = coalesce(nullif(trim($3), ''), name),
		       color = coalesce(nullif($4, ''), color),
		       archived_at = case
		         when $5::boolean is null then archived_at
		         when $5 then coalesce(archived_at, now())
		         else null end
		 where id = $1 and workspace_id = $2
		returning id, name, color, archived_at, 0, created_at`,
		labelID, workspaceID, deref(name), deref(color), archived).
		Scan(&l.ID, &l.Name, &l.Color, &l.ArchivedAt, &l.CampaignCount, &l.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

// SetCampaignLabels replaces the labels on one campaign.
func (r *Repo) SetCampaignLabels(
	ctx context.Context, campaignID, actor uuid.UUID, labelIDs []uuid.UUID,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`delete from public.campaign_label_assignments
		  where campaign_id = $1 and not (label_id = any($2))`,
		campaignID, labelIDs); err != nil {
		return err
	}
	for _, id := range labelIDs {
		if _, err := tx.Exec(ctx, `
			insert into public.campaign_label_assignments (campaign_id, label_id, assigned_by)
			values ($1, $2, $3) on conflict do nothing`, campaignID, id, actor); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// --- custom variables --------------------------------------------------------

// The application columns every scoped-content row carries, joined the same
// way so the two screens that show them cannot drift apart.
const appJoinColumns = `a.id, a.code, a.name, a.color`

// ListCustomVariables returns the placeholders this reader may see.
//
// `applicationIDs` nil means no narrowing (a Leader, or an unscoped caller);
// an empty non-nil slice means "this reader holds no application", which is
// not the same thing and must return only the workspace-wide rows.
func (r *Repo) ListCustomVariables(
	ctx context.Context, workspaceID uuid.UUID, applicationIDs []uuid.UUID, all bool,
) ([]models.CustomVariable, error) {
	rows, err := r.pool.Query(ctx, `
		select v.id, v.application_id, `+appJoinColumns+`,
		       v.key, v.label, v.default_value, v.description, v.is_active, v.created_at
		  from public.custom_variables v
		  left join public.applications a on a.id = v.application_id
		 where v.workspace_id = $1
		   and ($2::boolean or v.application_id is null or v.application_id = any ($3::uuid[]))
		 order by a.code nulls first, lower(v.key)`,
		workspaceID, all, applicationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.CustomVariable{}
	for rows.Next() {
		var v models.CustomVariable
		var appID *uuid.UUID
		if err := rows.Scan(&v.ID, &v.ApplicationID, &appID, &v.ApplicationCode,
			&v.ApplicationName, &v.ApplicationColor,
			&v.Key, &v.Label, &v.DefaultValue, &v.Description,
			&v.IsActive, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpsertCustomVariable creates or edits one, keyed by its placeholder name
// within its application.
//
// Two conflict targets rather than one, matching the two partial indexes in
// migration 0032: Postgres treats NULLs as distinct, so a single constraint on
// (workspace, application, key) would silently allow duplicate workspace-wide
// keys. The branch picks the index that applies.
func (r *Repo) UpsertCustomVariable(
	ctx context.Context, workspaceID, createdBy uuid.UUID, v models.CustomVariable,
) (*models.CustomVariable, error) {
	key := strings.ToLower(strings.TrimSpace(v.Key))

	conflict := `(workspace_id, lower(key)) where application_id is null`
	if v.ApplicationID != nil {
		conflict = `(workspace_id, application_id, lower(key)) where application_id is not null`
	}

	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.custom_variables
			(workspace_id, application_id, key, label, default_value, description, is_active, created_by)
		values ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), $7, $8)
		on conflict `+conflict+` do update
		   set label = excluded.label,
		       default_value = excluded.default_value,
		       description = excluded.description,
		       is_active = excluded.is_active
		returning id`,
		workspaceID, v.ApplicationID, key, strings.TrimSpace(v.Label), deref(v.DefaultValue),
		deref(v.Description), v.IsActive, createdBy).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return r.customVariableByID(ctx, workspaceID, id)
}

func (r *Repo) customVariableByID(
	ctx context.Context, workspaceID, id uuid.UUID,
) (*models.CustomVariable, error) {
	var v models.CustomVariable
	var appID *uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select v.id, v.application_id, `+appJoinColumns+`,
		       v.key, v.label, v.default_value, v.description, v.is_active, v.created_at
		  from public.custom_variables v
		  left join public.applications a on a.id = v.application_id
		 where v.id = $1 and v.workspace_id = $2`, id, workspaceID).
		Scan(&v.ID, &v.ApplicationID, &appID, &v.ApplicationCode, &v.ApplicationName,
			&v.ApplicationColor, &v.Key, &v.Label, &v.DefaultValue, &v.Description,
			&v.IsActive, &v.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v, nil
}

// DeleteCustomVariable removes a placeholder definition.
//
// Safe to delete outright, unlike a label: the values already rendered into sent
// messages are stored on the target rows, so removing the definition cannot
// change what anybody received.
func (r *Repo) DeleteCustomVariable(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`delete from public.custom_variables where id = $1 and workspace_id = $2`, id, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LogGPTGeneration records one use of the draft generator.
//
// No credential is written, and none can be: the table has no column for one.
func (r *Repo) LogGPTGeneration(
	ctx context.Context,
	workspaceID uuid.UUID,
	adminID *uuid.UUID,
	campaignID *uuid.UUID,
	model, prompt, result string,
	promptTokens, completionTokens int,
	status, failure string,
) error {
	_, err := r.pool.Exec(ctx, `
		insert into public.gpt_generation_logs
			(workspace_id, campaign_id, admin_id, model, prompt, result,
			 prompt_tokens, completion_tokens, status, failure_reason)
		values ($1, $2, $3, $4, $5, nullif($6, ''),
		        nullif($7, 0), nullif($8, 0), $9, nullif($10, ''))`,
		workspaceID, campaignID, adminID, model, prompt, result,
		promptTokens, completionTokens, status, failure)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
