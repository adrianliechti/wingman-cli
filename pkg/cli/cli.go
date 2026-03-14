package cli

type RunOptions struct {
	Path string
	Env  []string

	WingmanURL   string
	WingmanToken string
}
