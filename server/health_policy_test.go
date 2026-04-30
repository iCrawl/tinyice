package server

import (
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"go.uber.org/zap"
)

func TestNewServerLeavesDeadStreamAutoRemovalDisabled(t *testing.T) {
	logger.Init("error", false, "")
	s := NewServer(&config.Config{SetupComplete: true}, zap.NewNop().Sugar(), "test", "test", "")

	if s.HealthM.AutoRemoveDeadEnabled() {
		t.Fatal("expected dead stream auto-removal to be disabled by default")
	}
}
