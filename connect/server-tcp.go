package connect

import (
	"errors"
	"fmt"
	"github.com/timshannon/bolthold"
	"github.com/zgwit/iot-master/v2/model"
	"iot-master-gateway/db"
	"iot-master-gateway/dbus"
	"net"
	"time"
)

// ServerTCP TCP服务器
type ServerTCP struct {
	server *model.Server

	children map[string]*ServerTcpTunnel

	listener *net.TCPListener

	running bool
}

func newServerTCP(server *model.Server) *ServerTCP {
	svr := &ServerTCP{
		server:   server,
		children: make(map[string]*ServerTcpTunnel),
	}
	return svr
}

// Open 打开
func (server *ServerTCP) Open() error {
	if server.running {
		return errors.New("server is opened")
	}

	addr, err := net.ResolveTCPAddr("tcp", resolvePort(server.server.Addr))
	if err != nil {
		return err
	}
	server.listener, err = net.ListenTCP("tcp", addr)
	if err != nil {
		return err
	}

	server.running = true
	go func() {
		for {
			c, err := server.listener.AcceptTCP()
			if err != nil {
				//TODO 需要正确处理接收错误
				break
			}

			buf := make([]byte, 128)
			n := 0
			n, err = c.Read(buf)
			if err != nil {
				_ = c.Close()
				continue
			}
			data := buf[:n]
			sn := string(data)
			tunnel := model.Tunnel{
				ServerId: server.server.Id,
				Addr:     sn,
			}

			err = db.Store().FindOne(&tunnel, bolthold.Where("ServerId").Eq(server.server.Id).And("SN").Eq(sn))
			has := err == bolthold.ErrNotFound
			//has, err := db.Engine.Where("server_id=?", server.server.Id).And("addr", sn).Get(&tunnel)
			if err != nil {
				_ = dbus.Publish(fmt.Sprintf("server/%d/error", server.server.Id), []byte(err.Error()))
				continue
			}

			tunnel.Last = time.Now()
			tunnel.Remote = c.RemoteAddr().String()
			if !has {
				//保存一条新记录
				tunnel.Type = "server-tcp"
				tunnel.Name = sn
				tunnel.Addr = server.server.Addr
				tunnel.Protocol = server.server.Protocol
				//_, _ = db.Engine.InsertOne(&tunnel)
				tunnel.Created = time.Now()
				_ = db.Store().Insert(bolthold.NextSequence(), &tunnel)
			} else {
				//上线
				//_, _ = db.Engine.ID(tunnel.Id).Cols("last", "remote").Update(tunnel)
				_ = db.Store().Update(tunnel.Id, &tunnel)
			}
			_ = dbus.Publish(fmt.Sprintf("tunnel/%d/online", tunnel.Id), nil)

			tnl := newServerTcpTunnel(&tunnel, c)
			server.children[tunnel.Id] = tnl
		}

		server.running = false
	}()

	return nil
}

// Close 关闭
func (server *ServerTCP) Close() (err error) {
	//close tunnels
	if server.children != nil {
		for _, l := range server.children {
			_ = l.Close()
		}
	}
	return server.listener.Close()
}

// GetTunnel 获取连接
func (server *ServerTCP) GetTunnel(id string) Tunnel {
	return server.children[id]
}

func (server *ServerTCP) Running() bool {
	return server.running
}
