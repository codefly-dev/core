package shared

import "fmt"

func TypeOf[T any]() string {
	var t T
	return fmt.Sprintf("%T", t)
}
