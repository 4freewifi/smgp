package smgp

import (
	"github.com/golang/glog"
	"math/rand"
	"sync"
	"time"
)

type Config struct {
	Server   *Server `json:"server"`
	Remote   string  `json:"remote"`
	ClientID string  `json:"clientID"`
	Secret   string  `json:"secret"`
}

type Pool struct {
	Config  *Config
	pool    map[*Connection]interface{}
	rwmutex *sync.RWMutex
	keepers []chan int
}

func (t *Pool) Start(n int) {
	t.pool = make(map[*Connection]interface{})
	t.rwmutex = new(sync.RWMutex)
	t.keepers = make([]chan int, n)
	for i := 0; i < n; i++ {
		t.keepers[i] = make(chan int)
		go t.keeper(i, t.keepers[i])
	}
}

func (t *Pool) Stop() {
	for _, stop := range t.keepers {
		stop <- 1
		close(stop)
	}
}

func (t *Pool) Submit(src, dst, msg string, opt *SubmitOptions) (
	err error) {
	t.rwmutex.RLock()
	defer func() {
		t.rwmutex.RUnlock()
	}()
	n := len(t.pool)
	if n == 0 {
		err = ErrorNoConnection
		return
	}
	n = rand.Intn(n)
	i := 0
	for c, _ := range t.pool {
		if i == n {
			err = c.Submit(src, dst, msg, opt)
			break
		}
		i++
	}
	return
}

func (t *Pool) add(conn *Connection) {
	t.rwmutex.Lock()
	defer func() {
		t.rwmutex.Unlock()
	}()
	t.pool[conn] = nil
}

func (t *Pool) remove(conn *Connection) {
	t.rwmutex.Lock()
	defer func() {
		t.rwmutex.Unlock()
	}()
	delete(t.pool, conn)
}

func (t *Pool) keeper(id int, stop <-chan int) {
	comm := make(chan error)
	defer func() {
		close(comm)
	}()
	for {
		glog.Infof("#%d: Connecting...", id)
		conn, err := NewConnection(t.Config.Remote, comm)
		if err != nil {
			goto Next
		}
		err = conn.Login(t.Config.ClientID, t.Config.Secret,
			5*time.Second)
		if err != nil {
			conn.Close()
			goto Next
		}
		t.add(conn)
		select {
		case <-stop:
			conn.Close()
			return
		case err = <-comm:
			t.remove(conn)
			conn.Close()
		}
	Next:
		glog.Errorf("#%d: %s", id, err)
		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}
