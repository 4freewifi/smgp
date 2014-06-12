package main

import (
	"4free.com.tw/smgp"
	"flag"
	"time"
)

func main() {
	flag.Parse()
	conn := smgp.Connection{
		Address: "202.102.39.166:3058",
		// Address: "127.0.0.1:8890",
	}
	err := conn.Connect()
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	err = conn.Login("HB_aWIFI", "HB_aWIFI")
	if err != nil {
		panic(err)
	}
	for conn.Connected() {
		err = conn.Submit("02551744323", "13543810498", "測試啊測試",
			smgp.DefaultSubmitOptions)
		if err != nil {
			panic(err)
		}
		time.Sleep(time.Hour)
	}
	return
}
