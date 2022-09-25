package log

import (
	"fmt"
	api "github.com/2222-42/proglog/api/v1"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Log struct {
	mu            sync.RWMutex
	Dir           string
	Config        Config
	activeSegment *segment
	segments      []*segment
}

func NewLog(dir string, c Config) (*Log, error) {
	// set default value
	if c.Segment.MaxStoreBytes == 0 {
		c.Segment.MaxStoreBytes = 1024
	}

	if c.Segment.MaxIndexBytes == 0 {
		c.Segment.MaxIndexBytes = 1024
	}

	l := &Log{
		Dir:    dir,
		Config: c,
	}
	return l, l.setup()
}

func (l *Log) setup() error {
	files, err := os.ReadDir(l.Dir) // get a list of segments on disc
	if err != nil {
		return err
	}

	var baseOffsets []uint64
	for _, file := range files {
		// get the value of baseOffsets from file_name
		offStr := strings.TrimSuffix(
			file.Name(),
			path.Ext(file.Name()),
		)
		off, _ := strconv.ParseUint(offStr, 10, 0)
		baseOffsets = append(baseOffsets, off)
	}
	// 古い順にソート
	sort.Slice(baseOffsets, func(i, j int) bool {
		return baseOffsets[i] < baseOffsets[j]
	})

	// ディスク上のすでに存在するセグメントを処理して設定
	for i := 0; i < len(baseOffsets); i++ {
		if err = l.newSegment(baseOffsets[i]); err != nil {
			return err
		}

		// baseOffsetsはindexとstoreの2つの重複を含んでいるので、重複しているものをスキップ
		i++
	}
	// 既存のセグメントがなければ、最初のセグメントを作成。
	if l.segments == nil {
		if err = l.newSegment(
			l.Config.Segment.InitialOffset,
		); err != nil {
			return err
		}
	}
	return nil
}

func (l *Log) Append(record *api.Record) (uint64, error) {
	// Appendメソッドの処理を排他的にする。
	// ロックを獲得している書き込みがない場合、読み出しのアクセスを許可するためにRWMutexを使う。
	// REFACTOR: ログ全体ではなく、セグメントごとにロックを獲得する
	l.mu.Lock()
	defer l.mu.Unlock()

	highestOffset, err := l.highestOffset()
	if err != nil {
		return 0, err
	}

	if l.activeSegment.IsMaxed() {
		err = l.newSegment(highestOffset + 1)
		if err != nil {
			return 0, err
		}
	}

	off, err := l.activeSegment.Append(record)
	if err != nil {
		return 0, err
	}

	return off, nil
}

func (l *Log) Read(off uint64) (*api.Record, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var s *segment
	for _, segment := range l.segments {
		if segment.baseOffset <= off && off < segment.nextOffset {
			s = segment
			break
		}
	}

	if s == nil || s.nextOffset <= off {
		return nil, fmt.Errorf("offset out of range: %d", off)
	}

	return s.Read(off)
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, segment := range l.segments {
		if err := segment.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (l *Log) highestOffset() (uint64, error) {
	off := l.segments[len(l.segments)-1].nextOffset
	if off == 0 {
		return 0, nil
	}

	return off - 1, nil
}

func (l *Log) newSegment(off uint64) error {
	s, err := newSegment(l.Dir, off, l.Config)
	if err != nil {
		return err
	}
	l.segments = append(l.segments, s)
	l.activeSegment = s
	return nil
}
