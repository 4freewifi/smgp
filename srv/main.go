package main

import (
	"4free.com.tw/smgp"
	"encoding/json"
	"flag"
	"io/ioutil"
)

type Config struct {
	Server   *smgp.Server `json:"server"`
	Remote   string       `json:"remote"`
	ClientID string       `json:"clientID"`
	Secret   string       `json:"secret"`
}

func (t *Config) Load(filename string) (err error) {
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	err = json.Unmarshal(b, t)
	return
}

func main() {
	conf := flag.String("conf", "config.json", "config file in JSON format")
	flag.Parse()
	config := new(Config)
	err := config.Load(*conf)
	if err != nil {
		panic(err)
	}
	conn := &smgp.Connection{
		Address: config.Remote,
	}
	err = conn.Connect()
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	err = conn.Login(config.ClientID, config.Secret)
	if err != nil {
		panic(err)
	}
	err = config.Server.Serve(conn)
	if err != nil {
		panic(err)
	}
	return
}
