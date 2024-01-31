package runners

//
//import (
//	"bufio"
//	"bytes"
//	"context"
//	"fmt"
//	"io"
//	"os/exec"
//	"sync"
//	"time"
//
//	"github.com/codefly-dev/core/wool"
//
//	"strings"
//
//	"github.com/pkg/errors"
//)
//
//func RequireExec(bins ...string) ([]string, bool) {
//	var missing []string
//	for _, bin := range bins {
//		_, err := exec.LookPath(bin)
//		if err != nil {
//			missing = append(missing, bin)
//		}
//	}
//	return missing, len(missing) == 0
//}
//
//type RunnerEvent struct {
//	Err     error
//	Message string
//}
//
//type WrappedCmd struct {
//	cmd            *exec.Cmd
//	writer         io.WriteCloser
//	wg             sync.WaitGroup
//	runningContext context.Context
//}
//
//func NewWrappedCmd(cmd *exec.Cmd, runningContext context.Context, writer io.WriteCloser) (*WrappedCmd, error) {
//	w := &WrappedCmd{
//		cmd:            cmd,
//		runningContext: runningContext,
//		writer:         writer,
//	}
//	return w, nil
//}
//
//type WrappedCmdOutput struct {
//	PID    int
//	Events chan RunnerEvent
//}
//
//type BufferedWriterCloser struct {
//	*bufio.Writer
//}
//
//func (bwc *BufferedWriterCloser) Close() error {
//	if err := bwc.Flush(); err != nil {
//		return err
//	}
//	return nil
//}
//
//func (run *WrappedCmd) Start() (*WrappedCmdOutput, error) {
//	stdout, err := run.cmd.StdoutPipe()
//	if err != nil {
//		return nil, errors.Wrap(err, "cannot createAndWait stdout pipe")
//	}
//
//	stderr, err := run.cmd.StderrPipe()
//	if err != nil {
//		return nil, errors.Wrap(err, "cannot createAndWait stderr pipe")
//	}
//
//	events := make(chan RunnerEvent, 1)
//	out := &WrappedCmdOutput{Events: events}
//
//	var b bytes.Buffer
//	writer := bufio.NewWriter(&b)
//	errWriter := &BufferedWriterCloser{Writer: writer}
//
//	go ForwardLogs(run.runningContext, &run.wg, stdout, run.writer)
//	go ForwardLogs(run.runningContext, &run.wg, stderr, run.writer, errWriter)
//
//	err = run.cmd.Start()
//	if err != nil {
//		return nil, errors.Wrap(err, "cannot start command")
//	}
//	out.PID = run.cmd.Process.Pid
//	go func() {
//		err := run.cmd.Wait()
//		if err != nil {
//			out.Events <- RunnerEvent{Err: err, Message: b.String()}
//			return
//		}
//	}()
//	return out, nil
//}
//
//func (run *WrappedCmd) Run() error {
//	stdout, err := run.cmd.StdoutPipe()
//	if err != nil {
//		return errors.Wrap(err, "cannot create stdout pipe")
//	}
//
//	stderr, err := run.cmd.StderrPipe()
//	if err != nil {
//		return errors.Wrap(err, "cannot create stderr pipe")
//	}
//
//	go ForwardLogs(run.runningContext, &run.wg, stdout, run.writer)
//	go ForwardLogs(run.runningContext, &run.wg, stderr, run.writer)
//
//	return run.cmd.Run()
//}
//
//func (run *WrappedCmd) Stop() error {
//	w := wool.Get(run.runningContext).In("go/runner")
//	// Create a channel to signal when the WaitGroup is done
//	done := make(chan struct{})
//	go func() {
//		run.wg.Wait()
//		close(done)
//	}()
//
//	// Select on the done channel and a time.After channel
//	select {
//	case <-done:
//		// The WaitGroup finished in time, wait for the command to finish
//		return run.cmd.Wait()
//	case <-time.After(5 * time.Second):
//		w.Focus("we waited like morons")
//
//		// The WaitGroup did not finish in time, kill the command
//		if err := run.cmd.Process.Kill(); err != nil {
//			return fmt.Errorf("cannot kill process: %w", err)
//		}
//	}
//	return nil
//}
//
//func ForwardLogs(ctx context.Context, wg *sync.WaitGroup, r io.ReadCloser, ws ...io.WriteCloser) {
//	defer wg.Done()
//	w := wool.Get(ctx).In("go/runner")
//	wg.Add(1)
//	scanner := bufio.NewScanner(r)
//	for {
//		select {
//		case <-ctx.Done():
//			w.Focus("LOG FORWARD GOT A CANCEL")
//			return
//		default:
//			for scanner.Scan() {
//				for _, w := range ws {
//					_, _ = w.Write([]byte(strings.TrimSpace(scanner.Text())))
//				}
//			}
//
//			if err := scanner.Err(); err != nil {
//				for _, w := range ws {
//					_, _ = w.Write([]byte(strings.TrimSpace(err.Error())))
//				}
//			}
//
//			_ = r.Close()
//			return
//		}
//	}
//}
