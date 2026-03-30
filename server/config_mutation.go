package server

import (
	"errors"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

const defaultTokenSaveDelay = 60 * time.Second

var (
	errPendingUserNotFound       = errors.New("pending user not found")
	errPendingUserDeniedRecently = errors.New("pending user denied recently")
	errUsernameTaken             = errors.New("username already taken")
)

func (s *Server) mutateConfig(mutate func(*config.Config) error) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()

	if err := mutate(s.Config); err != nil {
		return err
	}

	return s.Config.SaveConfig()
}

func (s *Server) withConfigLock(mutate func(*config.Config)) {
	s.configMu.Lock()
	defer s.configMu.Unlock()

	mutate(s.Config)
}

func (s *Server) saveConfig() error {
	return s.mutateConfig(func(*config.Config) error {
		return nil
	})
}

func (s *Server) effectiveTokenSaveDelay() time.Duration {
	if s.tokenSaveDelay > 0 {
		return s.tokenSaveDelay
	}
	return defaultTokenSaveDelay
}
