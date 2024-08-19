package subscription

import (
	"fmt"
	"strings"

	"gopkg.in/ini.v1"
)

type subConf struct {
	Listen   string `ini:"listen"`
	Username string `ini:"username"`
	Password string `ini:"password"`

	Subs   subSubsConf   `ini:",omitempty"`
	Groups subGroupsConf `ini:",omitempty"`
	Chains subChainsConf `ini:",omitempty"`
}

type subSubsConf map[string]string

type subGroupsConf map[string][]string

type subChainsConf struct {
	Chains [][]string
}
type subtextChainsConf struct {
	Chains []string `delim:"-" ini:"chain,allowshadow"`
}

func GetDefaultConf() *subConf {
	return &subConf{
		Listen: ":8080",

		Subs:   make(subSubsConf),
		Groups: make(subGroupsConf),
		Chains: GetDefaultChainsConf(),
	}
}

func GetDefaultChainsConf() subChainsConf {
	return subChainsConf{Chains: make([][]string, 0)}
}

func UnmarshalSubsConfFromIni(section *ini.Section) (subSubsConf, error) {
	cfg := make(subSubsConf)
	for _, key := range section.Keys() {
		cfg[key.Name()] = key.Value()
	}
	return cfg, nil
}

func UnmarshalGroupsConfFromIni(section *ini.Section) (subGroupsConf, error) {
	cfg := make(subGroupsConf)
	for _, key := range section.Keys() {
		splitStrs := strings.Split(key.Value(), ",")
		for i := range splitStrs {
			splitStrs[i] = strings.TrimSpace(splitStrs[i])
		}
		cfg[key.Name()] = splitStrs
	}
	return cfg, nil
}

func UnmarshalChainsConfFromIni(section *ini.Section) (subChainsConf, error) {
	cfg := subtextChainsConf{Chains: make([]string, 0)}
	err := section.MapTo(&cfg)
	finalCfg := GetDefaultChainsConf()
	if err != nil {
		return finalCfg, err
	}
	for _, chain := range cfg.Chains {
		splitChain := strings.Split(chain, ",")
		for i := range splitChain {
			splitChain[i] = strings.TrimSpace(splitChain[i])
		}
		finalCfg.Chains = append(finalCfg.Chains, splitChain)
	}
	return finalCfg, nil
}

func LoadAllConfsFromIni(source interface{}) (*subConf, error) {
	f, err := ini.LoadSources(ini.LoadOptions{
		Insensitive:         true,
		InsensitiveSections: true,
		InsensitiveKeys:     true,
		IgnoreInlineComment: true,
		AllowBooleanKeys:    false,
	}, source)
	if err != nil {
		return nil, err
	}

	s, err := f.GetSection("common")
	if err != nil {
		return nil, fmt.Errorf("invalid configuration file, [common] section is missing")
	}

	cfg := GetDefaultConf()
	err = s.MapTo(cfg)
	if err != nil {
		return nil, err
	}

	if s, err := f.GetSection("subs"); err == nil {
		cfg.Subs, err = UnmarshalSubsConfFromIni(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse section [subs], err: %v", err)
		}
	}

	if s, err := f.GetSection("groups"); err == nil {
		cfg.Groups, err = UnmarshalGroupsConfFromIni(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse section [groups], err: %v", err)
		}
	}
	if s, err := f.GetSection("chains"); err == nil {
		cfg.Chains, err = UnmarshalChainsConfFromIni(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse section [chains], err: %v", err)
		}
	}

	return cfg, nil
}
