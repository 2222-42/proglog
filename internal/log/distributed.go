package log

import (
	"bytes"
	api "github.com/2222-42/proglog/api/v1"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

type DistributedLog struct {
	config  Config
	log     *Log
	raftLog *logStore
	raft    *raft.Raft
}

func NewDistributedLog(dataDir string, config Config) (*DistributedLog, error) {
	l := &DistributedLog{
		config: config,
	}
	if err := l.setupLog(dataDir); err != nil {
		return nil, err
	}
	if err := l.setupRaft(dataDir); err != nil {
		return nil, err
	}

	return l, nil
}
func (l *DistributedLog) setupLog(dataDir string) error {
	logDir := filepath.Join(dataDir, "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	var err error
	l.log, err = NewLog(logDir, l.config)
	if err != nil {
		return err
	}

	return nil
}

// setupRaft は、サーバーの設定を行、Raftのインスタンスを作成する
func (l *DistributedLog) setupRaft(dataDir string) error {
	var err error

	// 有限ステートマシン(Finite State Machine)を作成
	fsm := &fsm{log: l.log}

	// Raftのログストアを作成する
	logDir := filepath.Join(dataDir, "raft", "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	logConfig := l.config
	logConfig.Segment.InitialOffset = 1
	l.raftLog, err = newLogStore(logDir, logConfig)
	if err != nil {
		return err
	}

	// 安定ストアはkey-value store。クラスタの構成を保存する。
	stableStore, err := raftboltdb.NewBoltStore( // BoltはGo用の組み込み型永続key-value store
		filepath.Join(dataDir, "raft", "stable"),
	)
	if err != nil {
		return err
	}

	// スナップショットストアにより、復旧の際に、効率的かつリーダーへの負担も少なく済む。
	retain := 1
	snapshotStore, err := raft.NewFileSnapshotStore(
		filepath.Join(dataDir, "raft"),
		retain,
		os.Stderr,
	)
	if err != nil {
		return err
	}

	maxPool := 5
	timeout := 10 * time.Second
	transport := raft.NewNetworkTransport(
		l.config.Raft.StreamLayer,
		maxPool,
		timeout,
		os.Stderr,
	)

	config := raft.DefaultConfig()
	config.LocalID = l.config.Raft.LocalID
	if l.config.Raft.HeartbeatTimeout != 0 {
		config.HeartbeatTimeout = l.config.Raft.HeartbeatTimeout
	}
	if l.config.Raft.ElectionTimeout != 0 {
		config.ElectionTimeout = l.config.Raft.ElectionTimeout
	}
	if l.config.Raft.LeaderLeaseTimeout != 0 {
		config.LeaderLeaseTimeout = l.config.Raft.LeaderLeaseTimeout
	}
	if l.config.Raft.CommitTimeout != 0 {
		config.CommitTimeout = l.config.Raft.CommitTimeout
	}

	l.raft, err = raft.NewRaft(
		config,
		fsm,
		l.raftLog,
		stableStore,
		snapshotStore,
		transport,
	)
	if err != nil {
		return err
	}

	hasState, err := raft.HasExistingState(
		l.raftLog,
		stableStore,
		snapshotStore,
	)
	if err != nil {
		return err
	}
	if l.config.Raft.Bootstrap && !hasState {
		config := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      config.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		err = l.raft.BootstrapCluster(config).Error()
	}

	return err
}

// Append はサーバーのログに直接レコードを追加するのではなく、レコードをログに追加するようにFSMに指示するコマンドを適用するようにRaftに指示する
func (l *DistributedLog) Append(record *api.Record) (uint64, error) {
	res, err := l.apply(
		AppendRequestType,
		&api.ProduceRequest{Record: record},
	)
	if err != nil {
		return 0, err
	}

	return res.(*api.ProduceResponse).Offset, nil
}

// apply はRaftのAPIを内容しており、リクエストを適用し、そのレスポンスを返す。
func (l *DistributedLog) apply(reqType RequestType, req proto.Message) (interface{}, error) {
	var buf bytes.Buffer
	_, err := buf.Write([]byte{byte(reqType)})
	if err != nil {
		return nil, err
	}
	b, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	_, err = buf.Write(b)
	if err != nil {
		return nil, err
	}
	timeout := 10 * time.Second
	future := l.raft.Apply(buf.Bytes(), timeout)
	if future.Error() != nil { // Raftのレプリケーションに何か問題が発生した場合。サービスのエラーは返さない。FSMのApplyメソッドが返したものを返す。
		return nil, future.Error()
	}
	res := future.Response()
	if err, ok := res.(error); ok { // Goの慣習と異なり、単一の値を返すので、型アサーションで検査する。
		return nil, err
	}

	return res, nil
}

func (l *DistributedLog) Read(offset uint64) (*api.Record, error) {
	// 緩やかな一貫性で、サーバのログから読み出す。強い一貫性が必要なら、Raftを経由する。
	return l.log.Read(offset)
}

var _ raft.FSM = (*fsm)(nil)

type fsm struct {
	log *Log
}

type RequestType uint8

const AppendRequestType RequestType = 0

func (f fsm) Apply(record *raft.Log) interface{} {
	buf := record.Data
	reqType := AppendRequestType
	switch reqType {
	case AppendRequestType:
		return f.applyAppend(buf[1:])
	}
	return nil
}

func (f fsm) applyAppend(b []byte) interface{} {
	var req api.ProduceRequest
	err := proto.Unmarshal(b, &req)
	if err != nil {
		return err
	}

	offset, err := f.log.Append(req.Record)
	if err != nil {
		return err
	}

	return &api.ProduceResponse{Offset: offset}
}

func (f fsm) Snapshot() (raft.FSMSnapshot, error) {
	r := f.log.Reader()
	return &snapshot{reader: r}, nil
}

var _ raft.FSMSnapshot = (*snapshot)(nil)

type snapshot struct {
	reader io.Reader
}

func (s snapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := io.Copy(sink, s.reader); err != nil {
		_ = sink.Cancel()
		return err
	}

	return sink.Close()
}

func (s snapshot) Release() {}

func (f fsm) Restore(r io.ReadCloser) error {
	b := make([]byte, lenWidth)
	var buf bytes.Buffer
	for i := 0; ; i++ {
		_, err := io.ReadFull(r, b)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		size := int64(enc.Uint64(b))
		if _, err = io.CopyN(&buf, r, size); err != nil {
			return err
		}

		record := &api.Record{}
		if err = proto.Unmarshal(buf.Bytes(), record); err != nil {
			return err
		}

		if i == 0 {
			// 初期オフセットをスナップショットから読み取った最初のレコードのオフセットに設定し、ログのオフセットが一致するようにする
			f.log.Config.Segment.InitialOffset = record.Offset
			// ログをリセットする。
			if err := f.log.Reset(); err != nil {
				return err
			}
		}

		// スナップショットのレコードを読みこんで、新たなログを追加
		if _, err = f.log.Append(record); err != nil {
			return err
		}
		buf.Reset()
	}
	return nil
}

var _ raft.LogStore = (*logStore)(nil)

type logStore struct {
	*Log
}

func (l logStore) FirstIndex() (uint64, error) {
	return l.LowestOffset()
}

func (l logStore) LastIndex() (uint64, error) {
	return l.HighestOffset()
}

func (l logStore) GetLog(index uint64, log *raft.Log) error {
	in, err := l.Read(index)
	if err != nil {
		return err
	}
	log.Data = in.Value
	log.Index = in.Offset
	log.Type = raft.LogType(in.Type)
	log.Term = in.Term
	return nil
}

func (l logStore) StoreLog(log *raft.Log) error {
	return l.StoreLogs([]*raft.Log{log})
}

func (l logStore) StoreLogs(logs []*raft.Log) error {
	for _, log := range logs {
		if _, err := l.Append(&api.Record{
			Value: log.Data,
			Term:  log.Term,
			Type:  uint32(log.Type),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (l logStore) DeleteRange(min, max uint64) error {
	return l.Truncate(max)
}

func newLogStore(dir string, c Config) (*logStore, error) {
	log, err := NewLog(dir, c)
	if err != nil {
		return nil, err
	}
	return &logStore{log}, nil
}

type StreamLayer struct{}

func (s StreamLayer) Accept() (net.Conn, error) {
	//TODO implement me
	panic("implement me")
}

func (s StreamLayer) Close() error {
	//TODO implement me
	panic("implement me")
}

func (s StreamLayer) Addr() net.Addr {
	//TODO implement me
	panic("implement me")
}

func (s StreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	//TODO implement me
	panic("implement me")
}
