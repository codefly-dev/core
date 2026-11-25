// Package semanticcontract owns the compatibility identity of Codefly's
// body-free semantic index. Producers and consumers import this package so a
// contract generation cannot drift through duplicated string literals.
package semanticcontract

// Version is the one semantic-index contract emitted and accepted by the
// current Codefly stack. There is one version, v1, and it names the current
// format: producer and consumer change together, in the same release train.
const Version = "codefly.semantic-index/v1"
