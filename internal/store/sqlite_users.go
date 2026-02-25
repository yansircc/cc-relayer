package store

import (
	"context"
	"database/sql"
	"time"
)

// ---------------------------------------------------------------------------
// User operations
// ---------------------------------------------------------------------------

func (s *SQLiteStore) CreateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, token_hash, token_prefix, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.TokenHash, u.TokenPrefix, u.Status, u.CreatedAt.Unix())
	return err
}

func (s *SQLiteStore) GetUserByTokenHash(ctx context.Context, tokenHash string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, token_hash, token_prefix, status, created_at, last_active_at FROM users WHERE token_hash = ?`,
		tokenHash)
	return scanUser(row)
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, token_hash, token_prefix, status, created_at, last_active_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) UpdateUserStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET status = ? WHERE id = ?", status, id)
	return err
}

func (s *SQLiteStore) UpdateUserToken(ctx context.Context, id, tokenHash, tokenPrefix string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET token_hash = ?, token_prefix = ? WHERE id = ?", tokenHash, tokenPrefix, id)
	return err
}

func (s *SQLiteStore) UpdateUserLastActive(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET last_active_at = ? WHERE id = ?", time.Now().Unix(), id)
	return err
}

func scanUser(scanner interface{ Scan(...any) error }) (*User, error) {
	var (
		id, name, tokenHash, tokenPrefix, status string
		createdAt                                 int64
		lastActiveAt                              sql.NullInt64
	)
	err := scanner.Scan(&id, &name, &tokenHash, &tokenPrefix, &status, &createdAt, &lastActiveAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u := &User{
		ID:          id,
		Name:        name,
		TokenHash:   tokenHash,
		TokenPrefix: tokenPrefix,
		Status:      status,
		CreatedAt:   time.Unix(createdAt, 0).UTC(),
	}
	if lastActiveAt.Valid {
		t := time.Unix(lastActiveAt.Int64, 0).UTC()
		u.LastActiveAt = &t
	}
	return u, nil
}
