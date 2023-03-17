package log

import "github.com/hashicorp/raft"

type Config struct {
	Raft struct {
		raft.Config
		BindAddr    string
		StreamLayer *StreamLayer
		Bootstrap   bool // 自分自身を唯一の投票者として設定されたサーバをブートスラップし、それがリーダになるまでまつ。
	}
	Segment struct {
		MaxStoreBytes uint64
		MaxIndexBytes uint64
		InitialOffset uint64
	}
}
