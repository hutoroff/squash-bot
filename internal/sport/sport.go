package sport

type Sport string

const (
	Squash      Sport = "squash"
	Badminton   Sport = "badminton"
	TableTennis Sport = "table_tennis"
	Tennis      Sport = "tennis"
	Padel       Sport = "padel"
	Bowling     Sport = "bowling"
	Default           = Squash
)

type Info struct {
	DefaultPlayersPerCourt int
	MaxPlayersPerCourt     int
	UnitKind               string
	Emoji                  string
}

var infos = map[Sport]Info{
	Squash:      {2, 4, "court", "🏸"},
	Badminton:   {2, 4, "court", "🏸"},
	TableTennis: {2, 4, "table", "🏓"},
	Tennis:      {2, 4, "court", "🎾"},
	Padel:       {4, 4, "court", "🎾"},
	Bowling:     {6, 6, "lane", "🎳"},
}

var all = []Sport{Squash, Badminton, TableTennis, Tennis, Padel, Bowling}

func Valid(value string) bool {
	_, ok := infos[Sport(value)]
	return ok
}

func Get(value Sport) Info { return infos[value] }

func All() []Sport { return append([]Sport(nil), all...) }
