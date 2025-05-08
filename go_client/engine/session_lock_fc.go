package engine

import (
	"errors"
	"io"
	"os/exec"
)

// ClearPuller 清理拉流资源
func (s *Session) ClearPuller() {
	s.pullMu.Lock()
	defer s.pullMu.Unlock()
	if s.pullFFmpegCmd != nil && s.pullFFmpegCmd.Process != nil {
		_ = s.pullFFmpegCmd.Process.Kill()
		_ = s.pullFFmpegCmd.Wait()
	}
	s.pullFFmpegCmd = nil
	s.pullReader = nil
}

// ClearPusher 清理推流资源
func (s *Session) ClearPusher() {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.pushFFmpegCmd != nil && s.pushFFmpegCmd.Process != nil {
		_ = s.pushFFmpegCmd.Process.Kill()
		_ = s.pushFFmpegCmd.Wait()
	}
	if s.pushStdin != nil {
		_ = s.pushStdin.Close()
	}

	s.pushStdin = nil
	s.pushFFmpegCmd = nil

}

// ClearRecorder 清理录制资源
func (s *Session) ClearRecorder() {
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	if s.recordFFmpegCmd != nil && s.recordFFmpegCmd.Process != nil {
		_ = s.recordFFmpegCmd.Process.Kill()
		_ = s.recordFFmpegCmd.Wait()
	}
	if s.recordStdin != nil {
		_ = s.recordStdin.Close()
	}
	s.recordStdin = nil
	s.recordFFmpegCmd = nil
}

var (
	PullReaderIsNil   = errors.New("pull reader is nil")
	PushWriterIsNil   = errors.New("push writer is nil")
	RecordWriterIsNil = errors.New("record writer is nil")
)

func (s *Session) PullerRead(buf []byte) error {
	s.pullMu.Lock()
	reader := s.pullReader
	s.pullMu.Unlock()

	if reader == nil {
		return PullReaderIsNil
	}

	_, err := io.ReadFull(reader, buf)
	return err
}

func (s *Session) PusherWrite(buf []byte) error {
	s.pushMu.Lock()
	pushWriter := s.pushStdin
	s.pushMu.Unlock()
	if pushWriter == nil {
		return PushWriterIsNil
	}
	_, err := pushWriter.Write(buf)
	return err
}

func (s *Session) RecorderWrite(buf []byte) error {
	s.recordMu.Lock()
	recordWriter := s.recordStdin
	s.recordMu.Unlock()
	if recordWriter == nil {
		return RecordWriterIsNil
	}
	_, err := recordWriter.Write(buf)
	return err
}

// SetRecorder 设置录制资源
func (s *Session) SetRecorder(recordFFmpegCmd *exec.Cmd, recordStdin io.WriteCloser) {
	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	s.recordFFmpegCmd = recordFFmpegCmd
	s.recordStdin = recordStdin
}

// SetPuller 设置拉流资源
func (s *Session) SetPuller(pullCmd *exec.Cmd, pullReader io.Reader) {
	s.pullMu.Lock()
	defer s.pullMu.Unlock()
	s.pullFFmpegCmd = pullCmd
	s.pullReader = pullReader
}

func (s *Session) SetPusher(pushCmd *exec.Cmd, pushStdin io.WriteCloser) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	s.pushFFmpegCmd = pushCmd
	s.pushStdin = pushStdin
}
