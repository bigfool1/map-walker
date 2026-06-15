package storage

import (
	"fmt"
	"strings"

	"map-walker/internal/realtime"
)

// MaxPositionChunkSize MySQL 位置批量更新每块最大行数。
const MaxPositionChunkSize = 500

// SavePositionChunk 在一个事务内执行 ≤MaxPositionChunkSize 行的 bulk UPDATE ... JOIN。
// 空 updates 为 no-op。RowsAffected 仅诊断用途，不用于判断行是否存在。
func (db *DB) SavePositionChunk(updates []realtime.PositionUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	if len(updates) > MaxPositionChunkSize {
		return fmt.Errorf("chunk size %d exceeds max %d", len(updates), MaxPositionChunkSize)
	}

	query, args := buildPositionChunkSQL(updates)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin chunk transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("exec chunk update (%d rows): %w", len(updates), err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunk (%d rows): %w", len(updates), err)
	}
	return nil
}

// buildPositionChunkSQL 构建参数化 UPDATE ... JOIN 语句和参数切片。
// 生成的 SQL 结构：
//
//	UPDATE users AS u
//	JOIN (SELECT ? AS id, ? AS lat, ? AS lng UNION ALL SELECT ?, ?, ? ...) AS positions
//	  ON positions.id = u.id
//	SET u.last_lat = positions.lat, u.last_lng = positions.lng
func buildPositionChunkSQL(updates []realtime.PositionUpdate) (string, []any) {
	var b strings.Builder
	args := make([]any, 0, len(updates)*3)

	b.WriteString("UPDATE users AS u JOIN (")

	for i := range updates {
		if i > 0 {
			b.WriteString(" UNION ALL ")
		}
		b.WriteString("SELECT ? AS id, ? AS lat, ? AS lng")
		args = append(args, updates[i].UserID, updates[i].Lat, updates[i].Lng)
	}

	b.WriteString(") AS positions ON positions.id = u.id ")
	b.WriteString("SET u.last_lat = positions.lat, u.last_lng = positions.lng")

	return b.String(), args
}
