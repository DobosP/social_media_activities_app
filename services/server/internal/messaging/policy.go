package messaging

import "fmt"

// Policy can tighten the ciphertext and membership caps. It cannot enlarge the
// reviewed hard limits or alter cohort, blocking or guardian authority.
type Policy struct {
	MaxCiphertextBytes int
	MaxGroupMembers    int
}

func DefaultPolicy() Policy { return Policy{MaxCiphertextBytes: 65536, MaxGroupMembers: 256} }

func (p Policy) WithDefaults() Policy {
	d := DefaultPolicy()
	if p.MaxCiphertextBytes == 0 {
		p.MaxCiphertextBytes = d.MaxCiphertextBytes
	}
	if p.MaxGroupMembers == 0 {
		p.MaxGroupMembers = d.MaxGroupMembers
	}
	return p
}

func (p Policy) Validate() error {
	if p.MaxCiphertextBytes < 1 || p.MaxCiphertextBytes > 65536 {
		return fmt.Errorf("MESSAGING_MAX_CIPHERTEXT_BYTES must be between 1 and 65536")
	}
	if p.MaxGroupMembers < 2 || p.MaxGroupMembers > 256 {
		return fmt.Errorf("MESSAGING_MAX_GROUP_MEMBERS must be between 2 and 256")
	}
	return nil
}

// ConfigurePolicy is called once before registering HTTP/live handlers.
func (s *Service) ConfigurePolicy(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	s.MaxCiphertextBytes = p.MaxCiphertextBytes
	s.MaxGroupMembers = p.MaxGroupMembers
	return nil
}

func (s *Service) maxCiphertext() int {
	if s.MaxCiphertextBytes < 1 || s.MaxCiphertextBytes > 65536 {
		return 65536
	}
	return s.MaxCiphertextBytes
}
