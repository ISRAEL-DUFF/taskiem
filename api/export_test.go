package api

// Internals the external tests reach.
var SafeReturn = safeReturn

// WaitBackground waits for work done after answering (reset emails).
func (s *Server) WaitBackground() { s.bg.Wait() }
