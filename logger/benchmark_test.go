// Copyright 2012-2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logger

import (
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type discardWriter struct{}

func (d discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d discardWriter) Close() error                { return nil }
func (d discardWriter) Name() string                { return "/dev/null" }

// loggerOld represents the original logger implementation for comparison
type loggerOld struct {
	logger     *log.Logger
	debug      bool
	trace      bool
	infoLabel  string
	warnLabel  string
	errorLabel string
	fatalLabel string
	debugLabel string
	traceLabel string
}

func newOldLogger() *loggerOld {
	l := &loggerOld{
		logger: log.New(discardWriter{}, "", 0),
		debug:  true,
		trace:  true,
	}
	l.infoLabel = "[INF] "
	l.debugLabel = "[DBG] "
	l.warnLabel = "[WRN] "
	l.errorLabel = "[ERR] "
	l.fatalLabel = "[FTL] "
	l.traceLabel = "[TRC] "
	return l
}

func (l *loggerOld) Noticef(format string, v ...any) {
	l.logger.Printf(l.infoLabel+format, v...)
}

func (l *loggerOld) Debugf(format string, v ...any) {
	if l.debug {
		l.logger.Printf(l.debugLabel+format, v...)
	}
}

func (l *loggerOld) Tracef(format string, v ...any) {
	if l.trace {
		l.logger.Printf(l.traceLabel+format, v...)
	}
}

func newOptimizedLogger() *Logger {
	l := &Logger{
		logger: log.New(discardWriter{}, "", 0),
	}
	atomic.StoreInt32(&l.debug, 1)
	atomic.StoreInt32(&l.trace, 1)
	l.infoLabel = "[INF] "
	l.debugLabel = "[DBG] "
	l.warnLabel = "[WRN] "
	l.errorLabel = "[ERR] "
	l.fatalLabel = "[FTL] "
	l.traceLabel = "[TRC] "
	return l
}

// fileLoggerOld represents the original file logger implementation
type fileLoggerOld struct {
	f           writerAndCloser
	pid         string
	time        bool
	infoLabel   string
	errorLabel  string
}

func newOldFileLogger() *fileLoggerOld {
	return &fileLoggerOld{
		f:          discardWriter{},
		pid:        "[1234] ",
		time:       true,
		infoLabel:  "[INF] ",
		errorLabel: "[ERR] ",
	}
}

func (l *fileLoggerOld) logDirectOld(label, format string, v ...any) int {
	var entrya = [256]byte{}
	var entry = entrya[:0]
	if l.pid != "" {
		entry = append(entry, l.pid...)
	}
	if l.time {
		now := time.Now()
		year, month, day := now.Date()
		hour, min, sec := now.Clock()
		microsec := now.Nanosecond() / 1000
		entry = append(entry, fmt.Sprintf("%04d/%02d/%02d %02d:%02d:%02d.%06d ",
			year, month, day, hour, min, sec, microsec)...)
	}
	entry = append(entry, label...)
	entry = append(entry, fmt.Sprintf(format, v...)...)
	entry = append(entry, '\r', '\n')
	l.f.Write(entry)
	return len(entry)
}

func newOptimizedFileLogger() *fileLogger {
	fl := &fileLogger{
		f:          discardWriter{},
		pid:        []byte("[1234] "),
		time:       true,
		timeBuffer: make([]byte, 0, 32),
	}
	fl.l = &Logger{
		infoLabel:  "[INF] ",
		errorLabel: "[ERR] ",
	}
	return fl
}

func BenchmarkLoggerDebugCheck_Old(b *testing.B) {
	logger := newOldLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Debugf("test message %d", i)
	}
}

func BenchmarkLoggerDebugCheck_Optimized(b *testing.B) {
	logger := newOptimizedLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Debugf("test message %d", i)
	}
}

func BenchmarkLoggerTraceCheck_Old(b *testing.B) {
	logger := newOldLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Tracef("trace message %d", i)
	}
}

func BenchmarkLoggerTraceCheck_Optimized(b *testing.B) {
	logger := newOptimizedLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Tracef("trace message %d", i)
	}
}

func BenchmarkLoggerNotice_Old(b *testing.B) {
	logger := newOldLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Noticef("notice message %d", i)
	}
}

func BenchmarkLoggerNotice_Optimized(b *testing.B) {
	logger := newOptimizedLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Noticef("notice message %d", i)
	}
}

func BenchmarkFileLoggerDirect_Old(b *testing.B) {
	logger := newOldFileLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.logDirectOld(logger.infoLabel, "test message %d", i)
	}
}

func BenchmarkFileLoggerDirect_Optimized(b *testing.B) {
	logger := newOptimizedFileLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.logDirect(logger.l.infoLabel, "test message %d", i)
	}
}

func BenchmarkTimeFormatting_Old(b *testing.B) {
	now := time.Now()
	year, month, day := now.Date()
	hour, min, sec := now.Clock()
	microsec := now.Nanosecond() / 1000
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%04d/%02d/%02d %02d:%02d:%02d.%06d ",
			year, month, day, hour, min, sec, microsec)
	}
}

func BenchmarkTimeFormatting_Optimized(b *testing.B) {
	now := time.Now()
	year, month, day := now.Date()
	hour, min, sec := now.Clock()
	microsec := now.Nanosecond() / 1000
	buffer := make([]byte, 0, 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer = buffer[:0]
		_ = appendTime(buffer, year, month, day, hour, min, sec, microsec)
	}
}

func BenchmarkConcurrentDebugCheck_Old(b *testing.B) {
	logger := newOldLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			logger.Debugf("concurrent debug message %d", i)
			i++
		}
	})
}

func BenchmarkConcurrentDebugCheck_Optimized(b *testing.B) {
	logger := newOptimizedLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			logger.Debugf("concurrent debug message %d", i)
			i++
		}
	})
}

func BenchmarkConcurrentNotice_Old(b *testing.B) {
	logger := newOldLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			logger.Noticef("concurrent notice message %d", i)
			i++
		}
	})
}

func BenchmarkConcurrentNotice_Optimized(b *testing.B) {
	logger := newOptimizedLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			logger.Noticef("concurrent notice message %d", i)
			i++
		}
	})
}

func BenchmarkMemoryAllocation_Old(b *testing.B) {
	logger := newOldFileLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.logDirectOld(logger.infoLabel, "memory allocation test message with some data %d %s", i, "test")
	}
}

func BenchmarkMemoryAllocation_Optimized(b *testing.B) {
	logger := newOptimizedFileLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.logDirect(logger.l.infoLabel, "memory allocation test message with some data %d %s", i, "test")
	}
}

func BenchmarkRealFileLogger_Old(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "benchmark_old_*.log")
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()
	
	logger := &fileLoggerOld{
		f:          tmpFile,
		pid:        "[1234] ",
		time:       true,
		infoLabel:  "[INF] ",
		errorLabel: "[ERR] ",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.logDirectOld(logger.infoLabel, "real file test message %d", i)
	}
}

func BenchmarkRealFileLogger_Optimized(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "benchmark_opt_*.log")
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()
	
	logger := &fileLogger{
		f:          tmpFile,
		pid:        []byte("[1234] "),
		time:       true,
		timeBuffer: make([]byte, 0, 32),
	}
	logger.l = &Logger{
		infoLabel:  "[INF] ",
		errorLabel: "[ERR] ",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.logDirect(logger.l.infoLabel, "real file test message %d", i)
	}
}