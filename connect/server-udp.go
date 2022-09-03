package connect

import (
	"errors"
	"iot-master-gateway/db"
	"iot-master-gateway/model"
	"iot-master-gateway/pkg/events"
	"net"
	"time"
)

//ServerUDP UDP服务器
type ServerUDP struct {
	events.EventEmitter

	server *model.Server

	children map[uint64]*ServerUdpTunnel
	tunnels  map[string]*ServerUdpTunnel

	listener *net.UDPConn
	running  bool
}

func newServerUDP(server *model.Server) *ServerUDP {
	svr := &ServerUDP{
		server:   server,
		children: make(map[uint64]*ServerUdpTunnel),
		tunnels:  make(map[string]*ServerUdpTunnel),
	}
	return svr
}

//Open 打开
func (server *ServerUDP) Open() error {
	if server.running {
		return errors.New("server is opened")
	}
	server.Emit("open")

	addr, err := net.ResolveUDPAddr("udp", resolvePort(server.server.Addr))
	if err != nil {
		return err
	}
	c, err := net.ListenUDP("udp", addr)
	if err != nil {
		//TODO 需要正确处理接收错误
		return err
	}
	server.listener = c //共用连接

	server.running = true
	go func() {
		for {
			buf := make([]byte, 1024)
			n, addr, err := c.ReadFromUDP(buf)
			if err != nil {
				_ = c.Close()
				//continue
				break
			}
			data := buf[:n]

			//如果已经保存了链接 TODO 要有超时处理
			tnl, ok := server.tunnels[addr.String()]
			if ok {
				tnl.onData(data)
				continue
			}

			if !server.server.Register.Check(data) {
				_ = c.Close()
				continue
			}
			sn := string(data)
			tunnel := model.Tunnel{
				ServerId: server.server.Id,
				Addr:     sn,
			}

			err = db.Store().FindOne(&tunnel, bolthold.Where("ServerId").Eq(server.server.Id).And("SN").Eq(sn))
			has := err == bolthold.ErrNotFound
			//has, err := db.Engine.Where("server_id=?", server.server.Id).And("addr", sn).Get(&tunnel)
			if err != nil {
				_ = mqtt.Publish(fmt.Sprintf("server/%d/error", server.server.Id), []byte(err.Error()))
				continue
			}

			tunnel.Last = time.Now()
			tunnel.Remote = c.RemoteAddr().String()
			if !has {
				//保存一条新记录
				tunnel.Type = "server-udp"
				tunnel.Name = sn
				tunnel.Name = sn
				tunnel.SN = sn
				tunnel.Addr = server.server.Addr
				tunnel.Heartbeat = server.server.Heartbeat
				tunnel.Protocol = server.server.Protocol
				//_, _ = db.Engine.InsertOne(&tunnel)
				tunnel.Created = time.Now()
				_ = db.Store().Insert(bolthold.NextSequence(), &tunnel)
			} else {
				//上线
				//_, _ = db.Engine.ID(tunnel.Id).Cols("last", "remote").Update(tunnel)
				_ = db.Store().Update(tunnel.Id, &tunnel)
			}
			_ = mqtt.Publish(fmt.Sprintf("tunnel/%d/online", server.server.Id), nil)

			tnl = newServerUdpTunnel(&tunnel, c, addr)
			tnl.first = !has
			server.children[tunnel.Id] = tnl

			//启动对应的设备 发消息
			server.Emit("tunnel", tnl)

			tnl.Emit("online")

			tnl.Once("close", func() {
				delete(server.children, tunnel.Id)
				delete(server.tunnels, tnl.addr.String())
			})
		}

		server.running = false
	}()

	return nil
}

//Close 关闭
func (server *ServerUDP) Close() (err error) {
	server.Emit("close")
	//close tunnels
	if server.children != nil {
		for _, l := range server.children {
			_ = l.Close()
		}
	}
	return server.listener.Close()
}

//GetTunnel 获取链接
func (server *ServerUDP) GetTunnel(id uint64) link.Tunnel {
	return server.children[id]
}

func (server *ServerUDP) Running() bool {
	return server.running
}
