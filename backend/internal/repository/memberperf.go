package repository

import (
	"context"
	"fmt"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// The per-person tables on Performa, carrying the same figures as Rincian Per
// Hari rather than a smaller set of their own.
//
// Until now a person's row held eleven hand-picked metrics while the day by day
// table held forty-one, so the same screen measured a day one way and a person
// another. This runs the ordinary performance aggregate once per person, which
// is the only way the two can be guaranteed to agree: they are the same code
// reading the same tables with a different narrowing.

// maxMemberFanout bounds how many aggregates run at once. Same reasoning as the
// application split: each is a dozen indexed queries, and a workspace with
// thirty people should not take the connection pool from everyone else.
const maxMemberFanout = 4

// MemberBreakdown returns one full summary per person the caller may read.
//
// `role` narrows to one operational role, empty meaning all of them. It is not
// cosmetic: the caller pays an aggregate pass per row, and the Daftar PIC table
// draws only PICs, so computing the Freelance rows for it would be work nobody
// reads.
func (r *Repo) MemberBreakdown(
	ctx context.Context, sc Scope, f models.AnalyticsFilter, role string,
) ([]models.MemberBreakdown, error) {
	members, err := r.visibleMembers(ctx, sc, f)
	if err != nil {
		return nil, err
	}
	if role != "" {
		kept := members[:0]
		for _, m := range members {
			if m.Role == role {
				kept = append(kept, m)
			}
		}
		members = kept
	}
	if len(members) == 0 {
		return []models.MemberBreakdown{}, nil
	}

	onDuty, err := r.onDutyNow(ctx, sc.WorkspaceID)
	if err != nil {
		return nil, err
	}

	out := make([]models.MemberBreakdown, len(members))
	if err := fanOut(len(members), maxMemberFanout, func(i int) error {
		m := members[i]
		id := *m.UserID

		scoped := f
		scope := "personal"
		if m.Role == models.RolePIC {
			// A PIC is answerable for their applications, not only for what
			// they typed themselves, so their row is the whole of it: their own
			// work, their Freelance's, and the phone activity on those numbers.
			// This is the same narrowing the "Gabungan Tim PIC" toggle applies,
			// so opening the row shows the figures the row promised.
			scoped.PICUserID = &id
			scoped.AdminID = nil
			scope = "team"
		} else {
			scoped.AdminID = &id
			scoped.PICUserID = nil
		}

		report, err := r.Performance(ctx, sc, scoped)
		if err != nil {
			return fmt.Errorf("member %s: %w", m.Name, err)
		}

		out[i] = models.MemberBreakdown{
			UserID:         m.UserID,
			Name:           m.Name,
			Email:          m.Email,
			Role:           m.Role,
			IsActive:       m.IsActive,
			OnDuty:         onDuty[id],
			PICUserID:      m.PICUserID,
			PICName:        m.PICName,
			FreelanceCount: m.FreelanceCount,
			Applications:   m.Applications,
			Scope:          scope,
			Summary:        report.Summary,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}
