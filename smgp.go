package smgp

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	iconv "github.com/djimenez/iconv-go"
	"github.com/golang/glog"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	VERSION                = byte(0x30)
	REQID_LOGIN            = uint32(0x00000001)
	REQID_LOGIN_RESP       = uint32(0x80000001)
	REQID_SUBMIT           = uint32(0x00000002)
	REQID_SUBMIT_RESP      = uint32(0x80000002)
	REQID_DELIVER          = uint32(0x00000003)
	REQID_DELIVER_RESP     = uint32(0x80000003)
	REQID_ACTIVE_TEST      = uint32(0x00000004)
	REQID_ACTIVE_TEST_RESP = uint32(0x80000004)
	LOGIN_MODE_SEND        = byte(0x00)
	LOGIN_MODE_RECV        = byte(0x01)
	LOGIN_MODE_TRAN        = byte(0x02)
	STATUS_OK              = uint32(0)
	MSG_TYPE_MO            = byte(0x00)
	MSG_TYPE_MT            = byte(0x06)
	MSG_TYPE_P2P           = byte(0x07)
	SERVICE_ID_PC2P        = "PC2P\x00\x00\x00\x00\x00\x00"
	FEE_TYPE_FREE          = "00"
	FEE_TYPE_ONE           = "01"
	FEE_TYPE_MONTH         = "02"
	FEE_TYPE_MAX           = "03"
	ACTIVE_INTERVAL        = 3 * time.Minute
	ACTIVE_TIMEOUT         = time.Minute
	ACTIVE_RETRY           = 3
)

var ErrorNotMatch error = errors.New("RequestID and SequenceID do not match")
var ErrorNoConnection error = errors.New("No connection")
var ErrorServerTimeout error = errors.New("Server timeout")

type Connection struct {
	comm        chan<- error
	activeTimer *time.Timer
	activeRetry int
	activeSync  chan int
	connection  net.Conn
	sequenceID  uint32
	cSeq        chan uint32
	readSync    chan int
	writeSync   chan int
	pool        map[uint32]interface{}
}

type loginReq struct {
	Secret string
	AC     []byte
	Sync   chan int
}

func NewConnection(address string, comm chan<- error) (
	conn *Connection, err error) {
	connection, err := net.Dial("tcp", address)
	if err != nil {
		return
	}
	conn = new(Connection)
	conn.connection = connection
	conn.comm = comm
	conn.activeRetry = 0
	conn.activeSync = make(chan int, 1)
	conn.activeSync <- 1
	conn.activeTimer = time.AfterFunc(ACTIVE_INTERVAL, conn.keepActive)
	conn.sequenceID = 0
	conn.cSeq = make(chan uint32)
	go func() {
		// conn.cSeq may be closed
		defer func() {
			recover()
		}()
		for {
			conn.cSeq <- conn.sequenceID
			conn.sequenceID++
		}
	}()
	conn.pool = make(map[uint32]interface{})
	conn.readSync = make(chan int, 1)
	go conn.respHandler()
	conn.writeSync = make(chan int, 1)
	conn.writeSync <- 1
	glog.Info("Connected")
	return
}

func (t *Connection) Close() (err error) {
	for _, c := range []*chan int{
		&t.activeSync,
		&t.writeSync,
	} {
		_ = <-*c
		close(*c)
		*c = nil
	}
	if t.activeTimer != nil {
		t.activeTimer.Stop()
		t.activeTimer = nil
	}
	close(t.cSeq)
	err = t.connection.Close()
	_ = <-t.readSync
	t.pool = nil
	glog.Info("Closed")
	return
}

func (t *Connection) write(requestID uint32, body []byte, seq uint32) (
	err error) {
	data := []interface{}{
		uint32(len(body) + 12),
		requestID,
		seq,
	}
	buf := new(bytes.Buffer)
	for _, v := range data {
		err = binary.Write(buf, binary.BigEndian, v)
		if err != nil {
			return
		}
	}
	_, err = buf.Write(body)
	if err != nil {
		return
	}
	n, err := t.connection.Write(buf.Bytes())
	if err != nil {
		cerr, ok := err.(net.Error)
		if ok && cerr.Temporary() {
			return
		}
		t.comm <- err
		return
	}
	glog.V(1).Infof("Wrote %d bytes: %x", n, buf.Bytes()[4:buf.Len()])
	return
}

func (t *Connection) writeRequest(requestID uint32, body []byte) (
	seq uint32, err error) {
	_, ok := <-t.writeSync
	if !ok {
		err = ErrorNoConnection
		return
	}
	defer func() {
		t.writeSync <- 1
	}()
	seq = <-t.cSeq
	err = t.write(requestID, body, seq)
	return
}

func (t *Connection) writeResponse(requestID uint32, body []byte, seq uint32) (
	err error) {
	_, ok := <-t.writeSync
	if !ok {
		err = ErrorNoConnection
		return
	}
	defer func() {
		t.writeSync <- 1
	}()
	err = t.write(requestID, body, seq)
	return
}

func (t *Connection) read() (b []byte, err error) {
	b = make([]byte, 4)
	_, err = t.connection.Read(b)
	if err != nil {
		cerr, ok := err.(net.Error)
		if ok && cerr.Temporary() {
			return
		}
		t.comm <- err
		return
	}
	buf := bytes.NewBuffer(b)
	var l uint32
	err = binary.Read(buf, binary.BigEndian, &l)
	if err != nil {
		return
	}
	l -= 4 // already read 4 bytes
	b = make([]byte, l)
	n, err := t.connection.Read(b)
	if uint32(n) != l {
		err = errors.New("Unexpected EOF")
		return
	}
	glog.V(1).Infof("Read %d bytes: %x", l, b)
	return
}

func (t *Connection) respHandler() {
	defer func() {
		t.readSync <- 1
	}()
	for {
		b, err := t.read()
		if err != nil {
			glog.Error(err)
			return
		}
		t.resetActive()
		buf := bytes.NewBuffer(b)
		var req, seq uint32
		err = binary.Read(buf, binary.BigEndian, &req)
		if err != nil {
			glog.Error(err)
			continue
		}
		err = binary.Read(buf, binary.BigEndian, &seq)
		if err != nil {
			glog.Error(err)
			continue
		}
		switch req {
		case REQID_LOGIN_RESP:
			err = t.loginResp(seq, buf)
		case REQID_SUBMIT_RESP:
			err = t.submitResp(seq, buf)
		case REQID_ACTIVE_TEST:
			err = t.activeTestResp(seq, buf)
		case REQID_ACTIVE_TEST_RESP:
			// does nothing
		default:
			err = fmt.Errorf("Unsupported RequestID: %d", req)
		}
		if err != nil {
			glog.Error(err)
		}
	}
	return
}

func (t *Connection) getContext(seq uint32) (v interface{}, err error) {
	v, ok := t.pool[seq]
	if !ok {
		err = fmt.Errorf("Invalid SequenceID %d", seq)
	} else {
		delete(t.pool, seq)
	}
	return
}

// Login is synchronized. Set timeout in millisecond.
func (t *Connection) Login(clientID string, secret string,
	timeout time.Duration) (err error) {
	glog.Infof("Login %s secret %s", clientID, secret)
	// ClientID
	b, err := octetString(clientID, 8)
	if err != nil {
		return
	}
	body := bytes.NewBuffer(b)
	// AuthenticatorClient
	// MD5（ClientID + 7 字节的二进制 0（0x00） + Shared secret + Timestamp）
	md5 := md5.New()
	_, err = io.WriteString(md5, clientID)
	if err != nil {
		return
	}
	_, err = md5.Write([]byte{0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return
	}
	_, err = io.WriteString(md5, secret)
	if err != nil {
		return
	}
	timestamp := getTimestamp()
	_, err = io.WriteString(md5, timestamp)
	if err != nil {
		return
	}
	ac := md5.Sum(nil)
	glog.V(1).Infof("AuthenticatorClient 0x%x", ac)
	_, err = body.Write(ac)
	if err != nil {
		return
	}
	// login mode
	err = body.WriteByte(LOGIN_MODE_SEND)
	if err != nil {
		return
	}
	// timestamp in uint32
	its, err := strconv.ParseUint(timestamp, 10, 32)
	if err != nil {
		return
	}
	err = binary.Write(body, binary.BigEndian, uint32(its))
	if err != nil {
		return
	}
	// version
	err = body.WriteByte(VERSION)
	if err != nil {
		return
	}
	seq, err := t.writeRequest(REQID_LOGIN, body.Bytes())
	if err != nil {
		return
	}
	c := make(chan int)
	t.pool[seq] = &loginReq{
		Secret: secret,
		AC:     ac,
		Sync:   c,
	}
	// wait for respond
	select {
	case c <- 1:
	case <-time.After(timeout):
		err = errors.New("Login timed out")
		close(c)
	}
	return
}

func (t *Connection) loginResp(seq uint32, buf *bytes.Buffer) (
	err error) {
	cxt, err := t.getContext(seq)
	if err != nil {
		glog.Error(err)
		return
	}
	req, ok := cxt.(*loginReq)
	if !ok {
		err = ErrorNotMatch
		return
	}
	_, ok = <-req.Sync
	if !ok {
		err = errors.New("Login has timed out")
		return
	}
	close(req.Sync)
	// status
	var status uint32
	err = binary.Read(buf, binary.BigEndian, &status)
	if err != nil {
		return
	}
	if status != STATUS_OK {
		err = fmt.Errorf("Status = %d", status)
		return
	}
	// AuthenticatorServer
	// MD5（Status+AuthenticatorClient + Shared secret）
	as := make([]byte, 16)
	_, err = buf.Read(as)
	if err != nil {
		return
	}
	// according to SMGP dev, there's no need to verify
	// AuthenticatorServer.
	// md5 := md5.New()
	// err = binary.Write(md5, binary.BigEndian, status)
	// if err != nil {
	// 	return
	// }
	// glog.V(1).Infof("Stored AuthenticatorClient 0x%x", req.AC)
	// _, err = md5.Write(req.AC)
	// if err != nil {
	// 	return
	// }
	// glog.V(1).Infof("Stored Secret %s", req.Secret)
	// _, err = io.WriteString(md5, req.Secret)
	// if err != nil {
	// 	return
	// }
	// if bytes.Compare(as, md5.Sum(nil)) != 0 {
	// 	err = fmt.Errorf(
	// 		"Incorrect AuthenticatorServer 0x%x, expected 0x%x",
	// 		as, md5.Sum(nil))
	// 	return
	// }
	// version
	version, err := buf.ReadByte()
	if err != nil {
		return
	}
	if version < VERSION {
		err = fmt.Errorf("Server version 0x%x < client version 0x%x",
			version, VERSION)
		return
	}
	glog.Info("Login success")
	return
}

type SubmitOptions struct {
	MsgType   byte
	Priority  byte
	ServiceID string
	FeeType   string
	FeeCode   string
	FixedFee  string
}

var DefaultSubmitOptions *SubmitOptions = &SubmitOptions{
	MsgType:   MSG_TYPE_MT,
	Priority:  1,
	ServiceID: "PC2P",
	FeeType:   FEE_TYPE_FREE,
	FeeCode:   "\x00\x00\x00\x00\x00\x00",
	FixedFee:  "\x00\x00\x00\x00\x00\x00",
}

func (t *Connection) Submit(src, dst, msg string, opt *SubmitOptions) (
	err error) {
	glog.Infof("Submit %s -> %s: %s", src, dst, msg)
	body := new(bytes.Buffer)
	// MsgType
	err = body.WriteByte(opt.MsgType)
	if err != nil {
		return
	}
	// NeedReport = 0 for now
	err = body.WriteByte(0)
	if err != nil {
		return
	}
	// Priority
	if !(opt.Priority >= 0 && opt.Priority <= 3) {
		glog.Warningf("Incorrect priority %d, set to 1, normal.",
			opt.Priority)
		opt.Priority = 1
	}
	err = body.WriteByte(opt.Priority)
	if err != nil {
		return
	}
	// ServiceID 10 octetString
	b, err := octetString(opt.ServiceID, 10)
	if err != nil {
		return
	}
	_, err = body.Write(b)
	if err != nil {
		return
	}
	// FeeType 2 octetString
	if len(opt.FeeType) != 2 {
		err = errors.New("len(FeeType) should be 2")
		return
	}
	_, err = body.WriteString(opt.FeeType)
	if err != nil {
		return
	}
	// FeeCode 6 octetString
	b, err = octetString(opt.FeeCode, 6)
	if err != nil {
		return
	}
	_, err = body.Write(b)
	if err != nil {
		return
	}
	// FixedFee 6 octetString
	b, err = octetString(opt.FixedFee, 6)
	if err != nil {
		return
	}
	_, err = body.Write(b)
	if err != nil {
		return
	}
	// MsgFormat byte, text message required 15 for GB18030
	err = body.WriteByte(15)
	if err != nil {
		return
	}
	// ValidTime 17 octetString “YYMMDDhhmmsstnnp”
	_, err = body.Write([]byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return
	}
	// AtTime 17 octetString
	_, err = body.Write([]byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return
	}
	// SrcTermID 21 octetString
	b, err = octetString(src, 21)
	if err != nil {
		return
	}
	_, err = body.Write(b)
	if err != nil {
		return
	}
	// ChargeTermID 21 octetString
	_, err = body.Write([]byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0})
	if err != nil {
		return
	}
	// DestTermIDCount byte
	err = body.WriteByte(0x01)
	if err != nil {
		return
	}
	// DestTermID 21 octetString * count
	b, err = octetString(dst, 21)
	if err != nil {
		return
	}
	_, err = body.Write(b)
	if err != nil {
		return
	}
	// convert msg to GB18030
	msg, err = iconv.ConvertString(msg, "UTF-8", "GB18030")
	if err != nil {
		return
	}
	if len(msg) > 0xff {
		err = fmt.Errorf("Message too long: %d bytes", len(msg))
	}
	// MsgLength byte
	err = body.WriteByte(byte(len(msg)))
	if err != nil {
		return
	}
	// MsgContent octetString
	_, err = body.WriteString(msg)
	if err != nil {
		return
	}
	// Reserve 8 octetString
	_, err = body.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return
	}
	seq, err := t.writeRequest(REQID_SUBMIT, body.Bytes())
	if err != nil {
		return
	}
	t.pool[seq] = nil
	glog.Infof("Submit seq %d %s -> %s: %s", seq, src, dst, msg)
	return
}

func (t *Connection) submitResp(seq uint32, buf *bytes.Buffer) (
	err error) {
	_, err = t.getContext(seq)
	if err != nil {
		glog.Error(err)
		return
	}
	// MsgID 10 octetString
	msgID := make([]byte, 10)
	_, err = buf.Read(msgID)
	if err != nil {
		return
	}
	// status
	var status uint32
	err = binary.Read(buf, binary.BigEndian, &status)
	if err != nil {
		return
	}
	if status != STATUS_OK {
		err = fmt.Errorf("Submit seq %d failed, status = %d", seq, status)
	}
	glog.Infof("Submit seq %d MsgID 0x%x", seq, msgID)
	return
}

func (t *Connection) activeTest() (err error) {
	glog.V(1).Info("Send Active Test")
	_, err = t.writeRequest(REQID_ACTIVE_TEST, []byte{})
	return
}

func (t *Connection) activeTestResp(seq uint32, buf *bytes.Buffer) (
	err error) {
	glog.V(1).Info("Send Active Test Resp")
	err = t.writeResponse(REQID_ACTIVE_TEST_RESP, []byte{}, seq)
	return
}

func (t *Connection) keepActive() {
	_, ok := <-t.activeSync
	if !ok {
		return
	}
	defer func() {
		t.activeSync <- 1
	}()
	if t.activeRetry >= ACTIVE_RETRY {
		t.comm <- ErrorServerTimeout
		return
	}
	t.activeTest()
	t.activeRetry++
	t.activeTimer = time.AfterFunc(ACTIVE_TIMEOUT, t.keepActive)
	return
}

func (t *Connection) resetActive() {
	_, ok := <-t.activeSync
	if !ok {
		return
	}
	defer func() {
		t.activeSync <- 1
	}()
	t.activeRetry = 0
	t.activeTimer.Stop()
	t.activeTimer = time.AfterFunc(ACTIVE_INTERVAL, t.keepActive)
	return
}
