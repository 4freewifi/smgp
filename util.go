package smgp

import (
	"bytes"
	"fmt"
	"time"
)

func getTimestamp() string {
	now := time.Now()
	return fmt.Sprintf("%02d%02d%02d%02d%02d",
		int(now.Month()),
		now.Day(),
		now.Hour(),
		now.Minute(),
		now.Second(),
	)
}

func octetString(s string, l int) ([]byte, error) {
	b := bytes.NewBufferString(s)
	if b.Len() == l {
		return b.Bytes(), nil
	}
	if b.Len() < l {
		_, err := b.Write(bytes.Repeat([]byte{0}, l-b.Len()))
		return b.Bytes(), err
	}
	return nil, fmt.Errorf("string length %d > l %d", b.Len(), l)
}
