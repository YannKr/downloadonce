package db

import (
	"database/sql"
	"strings"

	"github.com/YannKr/downloadonce/internal/model"
)

func ListRecipientGroups(database *sql.DB, accountID string) ([]model.RecipientGroupSummary, error) {
	rows, err := database.Query(`
		SELECT g.id, g.account_id, g.name, g.description, g.created_at,
			COUNT(m.recipient_id) AS member_count
		FROM recipient_groups g
		LEFT JOIN recipient_group_members m ON m.group_id = g.id
		WHERE g.account_id = ?
		GROUP BY g.id
		ORDER BY g.name ASC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []model.RecipientGroupSummary
	for rows.Next() {
		var gs model.RecipientGroupSummary
		var createdAt SQLiteTime
		if err := rows.Scan(&gs.ID, &gs.AccountID, &gs.Name, &gs.Description, &createdAt, &gs.MemberCount); err != nil {
			return nil, err
		}
		gs.CreatedAt = createdAt.Time
		groups = append(groups, gs)
	}
	return groups, rows.Err()
}

func GetRecipientGroupByID(database *sql.DB, id string) (*model.RecipientGroup, error) {
	g := &model.RecipientGroup{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, name, description, created_at FROM recipient_groups WHERE id = ?`, id,
	).Scan(&g.ID, &g.AccountID, &g.Name, &g.Description, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.CreatedAt = createdAt.Time
	return g, nil
}

func CreateRecipientGroup(database *sql.DB, id, accountID, name, description string) error {
	_, err := database.Exec(
		`INSERT INTO recipient_groups (id, account_id, name, description) VALUES (?, ?, ?, ?)`,
		id, accountID, name, description,
	)
	return err
}

func UpdateRecipientGroup(database *sql.DB, id, accountID, name, description string) error {
	_, err := database.Exec(
		`UPDATE recipient_groups SET name = ?, description = ? WHERE id = ? AND account_id = ?`,
		name, description, id, accountID,
	)
	return err
}

func DeleteRecipientGroup(database *sql.DB, id, accountID string) error {
	_, err := database.Exec(
		`DELETE FROM recipient_groups WHERE id = ? AND account_id = ?`, id, accountID,
	)
	return err
}

func ListGroupMembers(database *sql.DB, groupID, accountID string) ([]model.RecipientGroupMember, error) {
	rows, err := database.Query(`
		SELECT r.id, r.account_id, r.name, r.email, r.org, r.created_at, m.added_at
		FROM recipient_group_members m
		JOIN recipients r ON r.id = m.recipient_id
		JOIN recipient_groups g ON g.id = m.group_id
		WHERE m.group_id = ? AND g.account_id = ?
		ORDER BY r.name ASC`, groupID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []model.RecipientGroupMember
	for rows.Next() {
		var m model.RecipientGroupMember
		var createdAt, addedAt SQLiteTime
		if err := rows.Scan(&m.ID, &m.AccountID, &m.Name, &m.Email, &m.Org, &createdAt, &addedAt); err != nil {
			return nil, err
		}
		m.CreatedAt = createdAt.Time
		m.AddedAt = addedAt.Time
		members = append(members, m)
	}
	return members, rows.Err()
}

func ListNonMembers(database *sql.DB, groupID string) ([]model.Recipient, error) {
	rows, err := database.Query(`
		SELECT r.id, r.account_id, r.name, r.email, r.org, r.created_at
		FROM recipients r
		WHERE r.id NOT IN (
			SELECT recipient_id FROM recipient_group_members WHERE group_id = ?
		)
		ORDER BY r.name ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recipients []model.Recipient
	for rows.Next() {
		var r model.Recipient
		var createdAt SQLiteTime
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Name, &r.Email, &r.Org, &createdAt); err != nil {
			return nil, err
		}
		r.CreatedAt = createdAt.Time
		recipients = append(recipients, r)
	}
	return recipients, rows.Err()
}

func AddGroupMember(database *sql.DB, groupID, recipientID string) error {
	_, err := database.Exec(
		`INSERT OR IGNORE INTO recipient_group_members (group_id, recipient_id) VALUES (?, ?)`,
		groupID, recipientID,
	)
	return err
}

func RemoveGroupMember(database *sql.DB, groupID, recipientID string) error {
	_, err := database.Exec(
		`DELETE FROM recipient_group_members WHERE group_id = ? AND recipient_id = ?`,
		groupID, recipientID,
	)
	return err
}

func ListGroupMemberIDs(database *sql.DB, groupID, accountID string) ([]string, error) {
	rows, err := database.Query(`
		SELECT m.recipient_id
		FROM recipient_group_members m
		JOIN recipient_groups g ON g.id = m.group_id
		WHERE m.group_id = ? AND g.account_id = ?`, groupID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func ListRecipientsWithGroups(database *sql.DB) ([]model.RecipientWithGroups, error) {
	rows, err := database.Query(`
		SELECT r.id, r.account_id, r.name, r.email, r.org, r.created_at,
			COALESCE(GROUP_CONCAT(g.id || '|' || g.name, '||'), '') AS groups
		FROM recipients r
		LEFT JOIN recipient_group_members m ON m.recipient_id = r.id
		LEFT JOIN recipient_groups g ON g.id = m.group_id
		GROUP BY r.id
		ORDER BY r.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []model.RecipientWithGroups
	for rows.Next() {
		var rwg model.RecipientWithGroups
		var createdAt SQLiteTime
		var groupsStr string
		if err := rows.Scan(&rwg.ID, &rwg.AccountID, &rwg.Name, &rwg.Email, &rwg.Org, &createdAt, &groupsStr); err != nil {
			return nil, err
		}
		rwg.CreatedAt = createdAt.Time
		if groupsStr != "" {
			for _, entry := range strings.Split(groupsStr, "||") {
				parts := strings.SplitN(entry, "|", 2)
				if len(parts) == 2 {
					rwg.Groups = append(rwg.Groups, model.GroupBadge{ID: parts[0], Name: parts[1]})
				}
			}
		}
		results = append(results, rwg)
	}
	return results, rows.Err()
}
