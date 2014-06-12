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
)

var ErrorNotMatch error = errors.New("RequestID and SequenceID are not matched")

type Connection struct {
	Address    string
	connection net.Conn
	connected  bool
	sequenceID uint32
	cSeq       chan uint32
	reader     chan int
	writer     chan int
	pool       map[uint32]interface{}
}

type loginReq struct {
	Secret string
	AC     []byte
}

func (t *Connection) Connect() (err error) {
	if t.connected {
		return
	}
	t.connection, err = net.Dial("tcp", t.Address)
	if err != nil {
		return
	}
	t.connected = true
	t.pool = make(map[uint32]interface{})
	t.cSeq = make(chan uint32)
	go func() {
		t.sequenceID = 0
		defer func() {
			close(t.cSeq)
		}()
		for t.connected {
			t.cSeq <- t.sequenceID
			t.sequenceID++
		}
	}()
	go t.respHandler()
	t.reader = make(chan int, 1)
	t.reader <- 1
	t.writer = make(chan int, 1)
	t.writer <- 1
	glog.Info("Connected")
	return
}

func (t *Connection) Close() (err error) {
	if !t.connected {
		return
	}
	t.connected = false
	_ = <-t.reader
	close(t.reader)
	_ = <-t.writer
	close(t.writer)
	t.pool = nil
	err = t.connection.Close()
	glog.Info("Close")
	return
}

func (t *Connection) Connected() bool {
	return t.connected
}

func (t *Connection) write(requestID uint32, body []byte) (
	seq uint32, err error) {
	_ = <-t.writer
	defer func() {
		t.writer <- 1
	}()
	seq = <-t.cSeq
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
		return
	}
	glog.Infof("Wrote %d bytes: %x", n, buf.Bytes()[4:buf.Len()])
	return
}

func (t *Connection) writeResp(requestID uint32, body []byte, seq uint32) (
	err error) {
	_ = <-t.writer
	defer func() {
		t.writer <- 1
	}()
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
		return
	}
	glog.Infof("Resp Wrote %d bytes: %x", n, buf.Bytes()[4:buf.Len()])
	return
}

func (t *Connection) read() (b []byte, err error) {
	b = make([]byte, 4)
	_, err = t.connection.Read(b)
	if err != nil {
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
	glog.Infof("Read %d bytes: %x", l, b)
	return
}

func (t *Connection) respHandler() {
	_ = <-t.reader
	defer func() {
		t.reader <- 1
	}()
	for t.connected {
		b, err := t.read()
		if err != nil {
			if err == io.EOF {
				glog.Warning("Connection closed by server")
				go t.Close()
				return
			}
			glog.Error(err)
			continue
		}
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
			err = t.handleLoginResp(seq, buf)
		case REQID_SUBMIT_RESP:
			err = t.handleSubmitResp(seq, buf)
		case REQID_ACTIVE_TEST:
			err = t.handleActiveTest(seq, buf)
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

func (t *Connection) Login(clientID string, secret string) (
	err error) {
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
	seq, err := t.write(REQID_LOGIN, body.Bytes())
	if err != nil {
		return
	}
	t.pool[seq] = &loginReq{
		Secret: secret,
		AC:     ac,
	}
	return
}

func (t *Connection) handleLoginResp(seq uint32, buf *bytes.Buffer) (
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
	md5 := md5.New()
	err = binary.Write(md5, binary.BigEndian, status)
	if err != nil {
		return
	}
	_, err = md5.Write(req.AC)
	if err != nil {
		return
	}
	_, err = io.WriteString(md5, req.Secret)
	if err != nil {
		return
	}
	if bytes.Compare(as, md5.Sum(nil)) != 0 {
		glog.Error("Incorrect AuthenticatorServer")
		//err = errors.New("Incorrect AuthenticatorServer")
		//return
	}
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
	MsgType:   MSG_TYPE_P2P,
	Priority:  1,
	ServiceID: "20140520",
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
		glog.Infof("Incorrect priority %d, set to 1, normal.",
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
	seq, err := t.write(REQID_SUBMIT, body.Bytes())
	if err != nil {
		return
	}
	t.pool[seq] = nil
	return
}

func (t *Connection) handleSubmitResp(seq uint32, buf *bytes.Buffer) (
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
	glog.Infof("MsgID 0x%x", msgID)
	// status
	var status uint32
	err = binary.Read(buf, binary.BigEndian, &status)
	if err != nil {
		return
	}
	if status != STATUS_OK {
		err = fmt.Errorf("Status = %d", status)
	}
	glog.Info("Submitted successfully")
	return
}

func (t *Connection) handleActiveTest(seq uint32, buf *bytes.Buffer) (
	err error) {
	glog.Info("Send Active_Test_Resp")
	err = t.writeResp(REQID_ACTIVE_TEST_RESP, []byte{}, seq)
	return
}
