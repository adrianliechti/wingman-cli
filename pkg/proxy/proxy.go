package proxy

type Config struct {
	Addr     string
	Upstream string
	Token    string

	User *UserInfo
}

type Proxy struct {
	Config
	Store *Store
}

func New(cfg Config) *Proxy {
	return &Proxy{
		Config: cfg,
		Store:  newStore(),
	}
}
