package smgp

import (
	"github.com/golang/glog"
	"github.com/gorilla/rpc"
	"github.com/gorilla/rpc/json"
	"net/http"
)

type Server struct {
	Addr string `json:"addr"`
}

type SMGP struct {
	Conn *Connection
}

type SubmitRequest struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Msg string `json:"msg"`
}

type SubmitResponse struct {
}

func (t *SMGP) Submit(req *SubmitRequest, res *SubmitResponse) (
	err error) {
	err = t.Conn.Submit(req.Src, req.Dst, req.Msg, DefaultSubmitOptions)
	if err != nil {
		return
	}
	return
}

func (t *Server) Serve(conn *Connection) (err error) {
	smgp := &SMGP{conn}
	s := rpc.NewServer()
	s.RegisterCodec(json.NewCodec(), "application/json")
	s.RegisterTCPService(smgp, "")
	if !s.HasMethod("SMGP.Submit") {
		return
	}
	http.Handle("/json-rpc", s)
	glog.Infof("ListenAndServe %s", t.Addr)
	err = http.ListenAndServe(t.Addr, nil)
	return
}
