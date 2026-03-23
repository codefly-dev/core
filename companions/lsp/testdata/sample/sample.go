// Package sample provides a minimal Go file for LSP tests.
package sample

// Greeter is a struct used to verify LSP document symbols.
type Greeter struct {
	Name string
}

// NewGreeter returns a new Greeter.
func NewGreeter(name string) *Greeter {
	return &Greeter{Name: name}
}

// Hello returns a greeting message.
func (g *Greeter) Hello() string {
	return "Hello, " + g.Name
}

const Version = "1.0.0"
