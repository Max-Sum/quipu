package router

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
	"github.com/Netflix/go-env"
	
)

type routerConf struct {
	ListenPlain string `ini:"listen_plain" env:"LISTEN_PLAIN"`
	ListenTLS   string `ini:"listen_tls" env:"LISTEN_TLS"`
	//listenQUIC    string   `ini:"listen_quic"`// TODO

	FinalHTTP  string `ini:"final_http" env:"FINAL_HTTP"`   // address:port / unix socket path
	FinalSocks string `ini:"final_socks" env:"FINAL_SOCKS"` // address:port / unix socket path
	FinalTLS   string `ini:"final_tls" env:"FINAL_TLS"`     // address:port / unix socket path

	EnableRedir  bool            `ini:"allow_redir" env:"ALLOW_REDIR"` // whether or not redir is enabled
	AllowPorts   string          `ini:"allow_ports" env:"ALLOW_PORTS"` // separated by comma, concatenated with -. eg. 80,443,10000-65535
	AllowPortmap [65536 / 8]byte `ini:"-,omitempty"  env:"-"`
}

func (c *routerConf) BuildPortmap() error {
	// Clear up
	c.AllowPortmap = [len(c.AllowPortmap)]byte{}
	allowports := strings.Split(c.AllowPorts, ",")
	for _, portr := range allowports {
		floor, ceil, ok := strings.Cut(portr, "-")
		if !ok {
			// Single port
			port, err := strconv.ParseUint(portr, 10, 16)
			if err != nil {
				return err
			}
			pos, rem := int(port/8), byte(port%8)
			c.AllowPortmap[pos] |= (byte(1) << rem)
			continue
		}
		floor = strings.TrimSpace(floor)
		ceil = strings.TrimSpace(ceil)
		// Port range
		flooru, err := strconv.ParseUint(floor, 10, 16)
		if err != nil {
			return err
		}
		ceilu, err := strconv.ParseUint(ceil, 10, 16)
		if err != nil {
			return err
		}
		if floor > ceil {
			err = fmt.Errorf("invalid port range: %s", portr)
			return err
		}
		floorpos, floorrem := int(flooru/8), byte(flooru%8)
		ceilpos, ceilrem := int(ceilu/8), byte(ceilu%8)
		floorbits := byte(0xff) << floorrem
		ceilbits := byte(0xff) >> (7 - ceilrem)
		fmt.Println(floorpos, ceilpos, floorrem, ceilrem)
		fmt.Println(floorbits, ceilbits)
		if floorpos == ceilpos {
			c.AllowPortmap[ceilpos] |= floorbits & ceilbits
		} else {
			c.AllowPortmap[floorpos] |= floorbits
			c.AllowPortmap[ceilpos] |= ceilbits
			for i := floorpos + 1; i < ceilpos; i++ {
				c.AllowPortmap[i] |= byte(0xff)
			}
		}
	}
	return nil
}

func (c *routerConf) IsPortAllowed(port uint16) bool {
	pos, rem := int(port/8), byte(port%8)
	return (c.AllowPortmap[pos] & (byte(1) << rem)) != 0
}

func GetDefaultConf() *routerConf {
	return &routerConf{}
}

func LoadAllConfsFromEnv() (*routerConf, error) {
	cfg := GetDefaultConf()
	_, err := env.UnmarshalFromEnviron(cfg)
	return cfg, err
}

func LoadAllConfsFromIni(source interface{}) (*routerConf, error) {
	f, err := ini.LoadSources(ini.LoadOptions{
		Insensitive:         true,
		InsensitiveSections: true,
		InsensitiveKeys:     true,
		IgnoreInlineComment: true,
		AllowBooleanKeys:    true,
	}, source)
	if err != nil {
		return nil, err
	}

	s, err := f.GetSection("")
	if err != nil {
		return nil, fmt.Errorf("invalid configuration file")
	}

	cfg, _ := LoadAllConfsFromEnv()
	if cfg == nil {
		cfg = GetDefaultConf()
	}
	err = s.MapTo(cfg)
	if err != nil {
		return nil, err
	}
	if err := cfg.BuildPortmap(); err != nil {
		return nil, fmt.Errorf("failed to build port bit map, err: %v", err)
	}

	return cfg, nil
}
