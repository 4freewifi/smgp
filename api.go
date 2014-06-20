package smgp

import (
	"github.com/golang/glog"
	"github.com/gorilla/rpc"
	"github.com/gorilla/rpc/json"
	"net/http"
)

type SubmitRequest struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Msg string `json:"msg"`
}

type SubmitResponse struct {
}

type Submit interface {
	Submit(src, dst, msg string, opt *SubmitOptions) error
}

type SMGP struct {
	smgp Submit
}

func (t *SMGP) Submit(req *SubmitRequest, res *SubmitResponse) (
	err error) {
	err = t.smgp.Submit(req.Src, req.Dst, req.Msg, DefaultSubmitOptions)
	if err != nil {
		return
	}
	return
}

type Server struct {
	Addr string `json:"addr"`
}

func (t *Server) Serve(srv Submit) (err error) {
	smgp := &SMGP{srv}
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
