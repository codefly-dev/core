package shared

import "fmt"

type ErrorResourceNotFound struct {
	resource     string
	resourceType string
}

func (e ErrorResourceNotFound) Error() string {
	return fmt.Sprintf("resource <%s> of type <%s> not found", e.resource, e.resourceType)
}

func NewErrorResourceNotFound(resourceType string, resource string) *ErrorResourceNotFound {
	return &ErrorResourceNotFound{resource: resource, resourceType: resourceType}
}
