package storage

// SaveScore 单调持久化分数，不会减少或重复累加
// RowsAffected == 0 视为成功（数据库已有相同或更高分数）
func (db *DB) SaveScore(userID int64, score int64) error {
	var query string
	if db.Driver() == "mysql" {
		query = `UPDATE users SET collectible_score = GREATEST(collectible_score, ?) WHERE id = ?`
	} else {
		query = `UPDATE users SET collectible_score = MAX(collectible_score, ?) WHERE id = ?`
	}
	_, err := db.Exec(query, score, userID)
	return err
}
