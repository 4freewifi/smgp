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
	defer t.rwmutex.RUnlock()
	poolsize := len(t.pool)
	if poolsize == 0 {
		err = ErrorNoConnection
		return
	}
	i, n := 0, rand.Intn(poolsize)
	for c, _ := range t.pool {
		if i == n {
			err = c.Submit(src, dst, msg, opt)
			break
		}
		i++
	}
	if i >= poolsize {
		glog.Fatalf("Unexpected. poolsize %d n %d i %d", poolsize, n, i)
	}
	return
}

func (t *Pool) add(conn *Connection) {
	t.rwmutex.Lock()
	defer t.rwmutex.Unlock()
	t.pool[conn] = nil
}

func (t *Pool) remove(conn *Connection) {
	t.rwmutex.Lock()
	defer t.rwmutex.Unlock()
	delete(t.pool, conn)
}

func (t *Pool) keeper(id int, stop <-chan int) {
	defer glog.Infof("#%d: Stopped", id)
	for {
		glog.Infof("#%d: Connecting...", id)
		c, err := NewConnection(t.Config.Remote)
		if err != nil {
			goto Next
		}
		err = c.Login(t.Config.ClientID, t.Config.Secret,
			5*time.Second)
		if err != nil {
			c.Close()
			goto Next
		}
		t.add(c)
		select {
		case <-stop:
			glog.Infof("#%d: Closing...", id)
			c.Close()
			return
		case <-c.Closed:
			t.remove(c)
		}
	Next:
		glog.Errorf("#%d: %v", id, err)
		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
			glog.Info("Try reconnecting every 10 secs")
		}
	}
}
