package http

import (
	"crypto/tls"
	"fmt"
)

type TLSInfo struct {
	CertFile string
	KeyFile  string

	ServerName string

	LocalAddr string
	// placeholder
}

func (info TLSInfo) Empty() bool {
	return info.CertFile == "" && info.KeyFile == ""
}

func (info TLSInfo) baseConfig() (*tls.Config, error) {
	if info.KeyFile == "" || info.CertFile == "" {
		return nil, fmt.Errorf("KeyFile and CertFile must both be present[key: %v, cert: %v]", info.KeyFile, info.CertFile)
	}
	// TODO
	return nil, nil
}

func (info TLSInfo) ClientConfig() (*tls.Config, error) {
	var cfg *tls.Config
	var err error

	if !info.Empty() {
		cfg, err = info.baseConfig()
		if err != nil {
			return nil, err
		}
	} else {
		cfg = &tls.Config{ServerName: info.ServerName}
	}
	return cfg, nil
}
