package main

import "testing"

func TestFileURLFromSlash(t *testing.T) {
	cases := []struct {
		name      string
		slashPath string
		want      string
	}{
		{
			name:      "unix absolute path",
			slashPath: "/home/fc/.local/share/fased/launch.html",
			want:      "file:///home/fc/.local/share/fased/launch.html",
		},
		{
			name:      "windows drive path",
			slashPath: "C:/Users/runneradmin/AppData/Roaming/fased-app/launch-123.html",
			want:      "file:///C:/Users/runneradmin/AppData/Roaming/fased-app/launch-123.html",
		},
		{
			name:      "windows drive path with spaces",
			slashPath: "D:/Fased App/launch file.html",
			want:      "file:///D:/Fased%20App/launch%20file.html",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fileURLFromSlash(tc.slashPath); got != tc.want {
				t.Fatalf("fileURLFromSlash(%q) = %q, want %q", tc.slashPath, got, tc.want)
			}
		})
	}
}
