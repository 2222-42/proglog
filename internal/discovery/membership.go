package discovery

import (
	"github.com/hashicorp/serf/serf"
	"go.uber.org/zap"
	"net"
)

type Membership struct {
	Config
	handler Handler
	serf    *serf.Serf
	events  chan serf.Event
	logger  *zap.Logger
}

func New(handler Handler, config Config) (*Membership, error) {
	membership := &Membership{
		Config:  config,
		handler: handler,
		logger:  zap.L().Named("membership"),
	}

	if err := membership.Setup(); err != nil {
		return nil, err
	}

	return membership, nil
}

type Config struct {
	NodeName       string
	BindAddr       string
	Tags           map[string]string
	StartJoinAddrs []string
}

func (m *Membership) Setup() error {
	addr, err := net.ResolveTCPAddr("tcp", m.BindAddr)
	if err != nil {
		return err
	}

	serfConfig := serf.DefaultConfig()
	serfConfig.Init()
	// BindAddrとBindPortはゴシッププロトコルを使うために必要
	serfConfig.MemberlistConfig.BindAddr = addr.IP.String()
	serfConfig.MemberlistConfig.BindPort = addr.Port
	// EventChはノードがクラスタに参加・離脱した時に、Serfのイベントを受信する手段
	m.events = make(chan serf.Event, 1024)
	serfConfig.EventCh = m.events
	serfConfig.Tags = m.Config.Tags         // クラスタ内の他のノードと共有し、このノードの処理方法をクラスタに知らせる簡単なデータのためにタグを使う必要がある
	serfConfig.NodeName = m.Config.NodeName // Serfクラスタ全体でノードの一意な識別子として機能
	m.serf, err = serf.Create(serfConfig)
	if err != nil {
		return err
	}
	go m.eventHandler()
	// 既存クラスタがあり、そのクラスタに追加したい新たなノードを作成したら、既存のクラスタないの少なくとも1つのノードに向ける必要がある。
	// 1つに接続したら、残りのノードについて知ることになる。
	if m.StartJoinAddrs != nil {
		_, err := m.serf.Join(m.StartJoinAddrs, true)
		if err != nil {
			return err
		}
	}

	return nil
}

type Handler interface {
	Join(name, addr string) error
	Leave(name string) error
}

// eventHandler はSerfからイベントチャンネルに送られてくるイベントを読み取り、それぞれのイベントに対応する処理を呼び出す
func (m *Membership) eventHandler() {
	for event := range m.events {
		switch event.EventType() {
		case serf.EventMemberJoin:
			for _, member := range event.(serf.MemberEvent).Members {
				// イベントが表すノードがローカルサーバであるなら、サーバが自分自身に作用しないようにする(e.g., 自分自身を複製しないようにする)
				if m.isLocal(member) {
					continue
				}

				m.handleJoin(member)
			}
		case serf.EventMemberLeave, serf.EventMemberFailed:
			for _, member := range event.(serf.MemberEvent).Members {
				if m.isLocal(member) {
					continue
				}

				m.handleLeave(member)
			}
		}
	}
}

func (m *Membership) handleJoin(member serf.Member) {
	if err := m.handler.Join(member.Name, member.Addr.String()); err != nil {
		m.logError(err, "failed to join", member)
	}
}

func (m *Membership) handleLeave(member serf.Member) {
	if err := m.handler.Leave(member.Name); err != nil {
		m.logError(err, "failed to leave", member)
	}
}

func (m *Membership) isLocal(member serf.Member) bool {
	return m.serf.LocalMember().Name == member.Name
}

func (m *Membership) Members() []serf.Member {
	return m.serf.Members()
}

func (m *Membership) Leave() error {
	return m.serf.Leave()
}

func (m *Membership) logError(err error, msg string, member serf.Member) {
	m.logger.Error(msg, zap.Error(err), zap.String("name", member.Name), zap.String("rpc_addr", member.Tags["rpc_addr"]))
}
