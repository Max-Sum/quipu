package subscription

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Max-Sum/quipu/knotchain"
	"github.com/Max-Sum/quipu/knotchain/knot"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type Server struct {
	conf *subConf
	http.Server
}

type ClashSub struct {
	Proxies []ClashProxy `yaml:"proxies"`
}

type ClashProxy struct {
	Name       string                 `yaml:"name"`
	ProxyType  string                 `yaml:"type"`
	Server     string                 `yaml:"server"`
	Port       int                    `yaml:"port"`
	SNI        string                 `yaml:"sni,omitempty"`
	Servername string                 `yaml:"servername,omitempty"`
	Others     map[string]interface{} `yaml:",inline"`
}

func NewServer(c *subConf) *Server {
	s := &Server{
		conf: c,
	}

	router := gin.Default()
	s.Server = http.Server{
		Addr:        c.Listen,
		Handler:     router,
		IdleTimeout: 30 * time.Minute,
	}
	var r *gin.RouterGroup
	if s.conf.Username != "" && s.conf.Password != "" {
		r = router.Group("/", gin.BasicAuth(gin.Accounts{s.conf.Username: s.conf.Password}))
	} else {
		r = router.Group("/")
	}

	r.GET("/clash", s.GetClashSub)

	return s
}

func (s *Server) GetClashSub(c *gin.Context) {
	groups, err := s.extractGroups()
	if err != nil {
		c.AbortWithError(500, err)
		return
	}
	sub := &ClashSub{Proxies: make([]ClashProxy, 0)}
	for _, gns := range s.conf.Chains.Chains {
		expandedSubs, err := expandChain(groups, gns)
		if err != nil {
			c.AbortWithError(500, err)
			return
		}
		for _, chain := range expandedSubs {

			if len(chain) == 0 {
				continue
			}
			tiedProxy, err := tieProxies(chain)
			if err != nil {
				c.AbortWithError(500, err)
				return
			}
			sub.Proxies = append(sub.Proxies, *tiedProxy)
		}
	}
	b, err := yaml.Marshal(sub)
	if err != nil {
		c.AbortWithError(500, err)
		return
	}
	c.Writer.Write(b)
}

func (s *Server) extractGroups() (map[string][]ClashProxy, error) {
	m := make(map[string][]ClashProxy)
	proxies := make([]ClashProxy, 0)
	for name, sublink := range s.conf.Subs {
		var r io.Reader
		if strings.HasPrefix(sublink, "http:") || strings.HasPrefix(sublink, "https:") {
			resp, err := http.Get(sublink)
			if err != nil {
				return nil, fmt.Errorf("get sub failed: [%s] : %v", name, err)
			}
			r = resp.Body
		} else {
			f, err := os.Open(sublink)
			if err != nil {
				return nil, fmt.Errorf("open sub file failed: [%s] : %v", name, err)
			}
			r = f
		}
		buf, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		// Open Yaml
		sub := &ClashSub{Proxies: make([]ClashProxy, 0)}
		err = yaml.Unmarshal(buf, sub)
		if err != nil {
			return nil, fmt.Errorf("unmarshal sub file failed: [%s] : %v", name, err)
		}
		proxies = append(proxies, sub.Proxies...)
		m[name] = sub.Proxies
	}
	for name, pns := range s.conf.Groups {
		m[name] = make([]ClashProxy, 0)
		for _, pn := range pns {
			found := false
			for _, proxy := range proxies {
				if pn == proxy.Name {
					m[name] = append(m[name], proxy)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("failed to find proxy [%s]", pn)
			}
		}
	}
	return m, nil
}

func expandChain(groupProxies map[string][]ClashProxy, chain []string) ([][]*ClashProxy, error) {
	var chainedProxies [][]*ClashProxy
	for _, groupName := range chain {
		group, ok := groupProxies[groupName]
		if !ok {
			return nil, fmt.Errorf("failed to expand chain: group [%s] is not found", groupName)
		}
		if len(group) == 0 {
			return nil, fmt.Errorf("failed to expand chain: group [%s] has no element", groupName)
		}
		if chainedProxies == nil {
			chainedProxies = addGroupToChain(nil, group)
			continue
		}
		nextChainedProxies := make([][]*ClashProxy, 0, len(chainedProxies)*len(group))
		for _, p := range chainedProxies {
			nextChainedProxies = append(nextChainedProxies, addGroupToChain(p, group)...)
		}
		chainedProxies = nextChainedProxies
	}
	return chainedProxies, nil
}

func addGroupToChain(chainedProxies []*ClashProxy, group []ClashProxy) [][]*ClashProxy {
	subs := make([][]*ClashProxy, len(group))
	for i := range group {
		// Initial Node
		if len(chainedProxies) == 0 {
			subs[i] = []*ClashProxy{&group[i]}
			continue
		}
		// Skip redundant nodes
		if group[i].Name == chainedProxies[len(chainedProxies)-1].Name {
			subs[i] = chainedProxies
			continue
		}
		newChained := make([]*ClashProxy, len(chainedProxies)+1)
		copy(newChained[:len(chainedProxies)], chainedProxies)
		newChained[len(newChained)-1] = &group[i]
		subs[i] = newChained
	}
	return subs
}

func tieProxies(chainedProxies []*ClashProxy) (*ClashProxy, error) {
	if len(chainedProxies) == 1 {
		return chainedProxies[0], nil
	} else if len(chainedProxies) == 0 {
		return nil, fmt.Errorf("no proxies to tie")
	}
	kchain := &knotchain.KnotChain{
		Version: knotchain.Version1,
		Knots:   make([]knotchain.Knot, len(chainedProxies)-1),
	}
	lastProxy := chainedProxies[len(chainedProxies)-1]
	nProxy := *lastProxy
	nProxy.Name = chainedProxies[0].Name
	nProxy.Server = chainedProxies[0].Server
	nProxy.Port = chainedProxies[0].Port
	for i, proxy := range chainedProxies[1:] {
		addr := net.ParseIP(proxy.Server)
		if addr != nil {
			kchain.Knots[i] = &knot.IP{
				Addr:  addr,
				IPort: uint16(proxy.Port),
			}
		} else {
			if i == len(kchain.Knots)-1 {
				sni := ""
				if len(proxy.SNI) > 0 {
					sni = proxy.SNI
				} else if len(proxy.Servername) > 0 {
					sni = proxy.Servername
				}
				if len(sni) > 0 && sni == proxy.Server {
					kchain.Knots[i] = &knot.Refer{Domain: knot.Domain{
						Addr:  proxy.Server,
						IPort: uint16(proxy.Port),
					}}
					continue
				}
			}
			kchain.Knots[i] = &knot.Domain{
				Addr:  proxy.Server,
				IPort: uint16(proxy.Port),
			}
		}
		nProxy.Name += "➜" + proxy.Name
	}
	if len(nProxy.SNI) > 0 {
		nsni, err := knotchain.TieChainToHostname(kchain, nProxy.SNI)
		if err != nil {
			return nil, err
		}
		nProxy.SNI = nsni
	} else if len(nProxy.Servername) > 0 {
		nsni, err := knotchain.TieChainToHostname(kchain, nProxy.Servername)
		if err != nil {
			return nil, err
		}
		nProxy.Servername = nsni
	} else {
		return nil, fmt.Errorf("unsupported proxy without sni: [%s]", lastProxy.Name)
	}
	return &nProxy, nil
}
