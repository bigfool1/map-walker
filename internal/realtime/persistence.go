package realtime

type PositionUpdate struct {
	UserID int64
	Lat    float64
	Lng    float64
	Seq    uint64
}

type PositionPersister interface {
	Submit(updates []PositionUpdate)
	Stop()
}

type PositionDrainer interface {
	PositionPersister
	Drain()
}

// ScoreUpdate 是 Hub 拾取后提交的不可变分数快照
type ScoreUpdate struct {
	UserID int64
	Score  int64
}

// ScorePersister 异步持久化分数，合并重复提交
type ScorePersister interface {
	Submit(update ScoreUpdate)
	SubmitSync(userID int64, score int64)
	Drain()
}
