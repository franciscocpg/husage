//go:build !unix

package stderr

// ToFile leaves standard error as it is: only macOS needs the redirect.
func ToFile(string) *Redirect { return nil }

// Restore does nothing: ToFile never redirects here.
func (r *Redirect) Restore(bool) {}
