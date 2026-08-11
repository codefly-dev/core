// Package semanticcontract owns the compatibility identity of Codefly's
// body-free semantic index. Producers and consumers import this package so a
// contract generation cannot drift through duplicated string literals.
package semanticcontract

// Version is the one semantic-index contract emitted and accepted by the
// current Codefly stack. A format or interpretation change must advance this
// value and update producer and consumer behavior in the same release train.
const Version = "codefly.semantic-index/v4"
