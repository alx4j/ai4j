package darwin

type deniedExecutableCandidate struct {
	path     string
	required bool
}

func mvpDeniedExecutableCandidates() []deniedExecutableCandidate {
	return []deniedExecutableCandidate{
		{path: "/bin/sh", required: true},
		{path: "/bin/zsh", required: true},
		{path: "/usr/bin/env", required: true},
		{path: "/usr/bin/osascript", required: true},
		{path: "/bin/bash"},
		{path: "/bin/csh"},
		{path: "/bin/tcsh"},
	}
}
