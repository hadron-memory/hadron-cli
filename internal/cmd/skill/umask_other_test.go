//go:build !unix

package skill

func syscallUmask(m int) int { return 0 }
