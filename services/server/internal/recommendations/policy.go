package recommendations

func (s *Service) savedSearchCap() int {
	if s.MaxSavedSearches >= 1 && s.MaxSavedSearches <= 100 {
		return s.MaxSavedSearches
	}
	return 20
}
