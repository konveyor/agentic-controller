package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var logger *zap.SugaredLogger

func init() {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(cfg),
		zapcore.AddSync(os.Stderr),
		zapcore.DebugLevel,
	)
	logger = zap.New(core).Sugar()
}

func Info(format string, a ...any) {
	logger.Infof(format, a...)
}

func Ok(format string, a ...any) {
	logger.Infof("[ok] "+format, a...)
}

func Warn(format string, a ...any) {
	logger.Warnf(format, a...)
}

func Err(format string, a ...any) {
	logger.Errorf(format, a...)
}

func Fatal(format string, a ...any) {
	logger.Fatalf(format, a...)
}

func Header(format string, a ...any) {
	logger.Infof("[header] "+format, a...)
}
