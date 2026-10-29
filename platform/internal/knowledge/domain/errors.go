package domain

import "errors"

// Domain errors. The HTTP adapter maps each to a problem code.
var (
	ErrUnitRetired = errors.New("knowledge: the TM unit is already retired")
	ErrInvalidUnit = errors.New("knowledge: invalid TM unit")

	ErrInvalidConcept      = errors.New("knowledge: invalid concept")
	ErrInvalidTerm         = errors.New("knowledge: invalid term")
	ErrInvalidTermStatus   = errors.New("knowledge: term status must be preferred, admitted, deprecated or forbidden")
	ErrInvalidPartOfSpeech = errors.New("knowledge: part_of_speech must be noun, verb, adjective, adverb, proper_noun, phrase or other")
	ErrDuplicateTerm       = errors.New("knowledge: a concept lists each term once per locale")

	ErrInvalidStyleGuide = errors.New("knowledge: invalid style guide")
	ErrInvalidStyleRule  = errors.New("knowledge: invalid style rule")
	ErrNamespaceScope    = errors.New("knowledge: a namespace style guide belongs to a project")
)
