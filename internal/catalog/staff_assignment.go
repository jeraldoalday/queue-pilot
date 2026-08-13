package catalog

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

func ListOrgStaff(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]sqlc.ListStaffUsersByOrgRow, error) {
	return sqlc.New(pool).ListStaffUsersByOrg(ctx, db.MustUUID(orgID))
}

// AssignedStaffIDs returns the set of staff user IDs currently assigned to
// serviceID, for checking checkbox state in the assignment UI.
func AssignedStaffIDs(ctx context.Context, pool *pgxpool.Pool, orgID, serviceID string) (map[string]bool, error) {
	rows, err := sqlc.New(pool).ListStaffForService(ctx, sqlc.ListStaffForServiceParams{
		ServiceID: db.MustUUID(serviceID),
		OrgID:     db.MustUUID(orgID),
	})
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(rows))
	for _, r := range rows {
		set[db.UUIDString(r.ID)] = true
	}
	return set, nil
}

// AllAssignments returns, for every service in orgID, the set of assigned
// staff user IDs — one batch query for the whole services page instead of
// one query per row.
func AllAssignments(ctx context.Context, pool *pgxpool.Pool, orgID string) (map[string]map[string]bool, error) {
	rows, err := sqlc.New(pool).ListStaffServiceAssignmentsByOrg(ctx, db.MustUUID(orgID))
	if err != nil {
		return nil, err
	}
	byService := make(map[string]map[string]bool)
	for _, r := range rows {
		sid := db.UUIDString(r.ServiceID)
		if byService[sid] == nil {
			byService[sid] = make(map[string]bool)
		}
		byService[sid][db.UUIDString(r.StaffUserID)] = true
	}
	return byService, nil
}

// ToggleStaffAssignment flips whether staffUserID can perform serviceID.
// orgID scopes both the read and write, so this can never touch another
// org's staff_services row even if serviceID/staffUserID were guessed.
func ToggleStaffAssignment(ctx context.Context, pool *pgxpool.Pool, orgID, serviceID, staffUserID string) error {
	q := sqlc.New(pool)

	assigned, err := q.IsStaffAssignedToService(ctx, sqlc.IsStaffAssignedToServiceParams{
		StaffUserID: db.MustUUID(staffUserID),
		ServiceID:   db.MustUUID(serviceID),
		OrgID:       db.MustUUID(orgID),
	})
	if err != nil {
		return err
	}

	if assigned {
		return q.UnassignStaffService(ctx, sqlc.UnassignStaffServiceParams{
			StaffUserID: db.MustUUID(staffUserID),
			ServiceID:   db.MustUUID(serviceID),
			OrgID:       db.MustUUID(orgID),
		})
	}
	return q.AssignStaffService(ctx, sqlc.AssignStaffServiceParams{
		OrgID:       db.MustUUID(orgID),
		StaffUserID: db.MustUUID(staffUserID),
		ServiceID:   db.MustUUID(serviceID),
	})
}
