package store

import (
	"context"
	"fmt"
)

// ImageURL returns an API URL; neither host paths nor original filenames are exposed.
func ImageURL(kind, file string) string {
	if file == "" {
		return ""
	}
	return "/api/account/images/" + kind + "/" + file
}

func imageColumn(kind string) (string, string, error) {
	switch kind {
	case "avatars":
		return "users", "avatar_file", nil
	case "teams":
		return "teams", "icon_file", nil
	default:
		return "", "", fmt.Errorf("unknown image kind")
	}
}

// SwapImage serializes replacements so each successful writer can remove the previous file.
// The caller authorizes the account or team before calling this method.
func (s *Store) SwapImage(ctx context.Context, kind, id, file string) (string, error) {
	table, column, err := imageColumn(kind)
	if err != nil {
		return "", err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var old string
	if err = tx.QueryRow(ctx, `SELECT `+column+` FROM `+table+` WHERE id=$1 FOR UPDATE`, id).Scan(&old); err != nil {
		return "", noRows(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE `+table+` SET `+column+`=$2 WHERE id=$1`, id, file); err != nil {
		return "", err
	}
	return old, tx.Commit(ctx)
}

// Images are visible to their owner and teammates; platform admins can also see them.
func (s *Store) ImageVisible(ctx context.Context, kind, file, user string, admin bool) (bool, error) {
	var visible bool
	var err error
	switch kind {
	case "avatars":
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u WHERE avatar_file=$1 AND ($3 OR u.id=$2 OR EXISTS(SELECT 1 FROM team_memberships a JOIN team_memberships b ON a.team_id=b.team_id WHERE a.user_id=u.id AND b.user_id=$2)))`, file, user, admin).Scan(&visible)
	case "teams":
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams t WHERE icon_file=$1 AND ($3 OR EXISTS(SELECT 1 FROM team_memberships m WHERE m.team_id=t.id AND m.user_id=$2)))`, file, user, admin).Scan(&visible)
	}
	return visible, err
}
