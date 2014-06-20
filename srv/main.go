package main

import (
	"4free.com.tw/smgp"
	"encoding/json"
	"flag"
	"io/ioutil"
)

func loadConfig(filename string) (conf *smgp.Config, err error) {
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	conf = new(smgp.Config)
	err = json.Unmarshal(b, conf)
	return
}

func main() {
	conf := flag.String("conf", "config.json", "config file in JSON format")
	flag.Parse()
	config, err := loadConfig(*conf)
	if err != nil {
		panic(err)
	}
	pool := &smgp.Pool{
		Config: config,
	}
	pool.Start(5)
	err = config.Server.Serve(pool)
	if err != nil {
		panic(err)
	}
	return
}
