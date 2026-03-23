# Communication between CLI and Plugin

Bidirectional gRPC streaming for interactive Q&A.

### Flow

1. CLI opens a `Communicate` bidirectional stream to the plugin
2. Plugin sends `Question` messages (confirm, input, selection, choice)
3. CLI answers each question via the `AnswerProvider` interface
4. CLI sends `Answer` messages back on the stream
5. Plugin stores answers and closes the stream when done

### Plugin side

```go
func (s *Builder) Communicate(stream builderv0.Builder_CommunicateServer) error {
    asker := communicate.NewQuestionAsker(stream)
    answers, err := asker.RunSequence(s.Options())
    s.answers = answers
    return err
}
```

### CLI side

```go
stream, _ := instance.Builder.Communicate(ctx)
communicate.Do(ctx, stream, answerProvider)
```
