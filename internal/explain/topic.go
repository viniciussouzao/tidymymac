package explain

// Topic is a macOS storage concept a user can ask about -- one of the buckets
// Storage settings shows, not anything TidyMyMac itself defines. Unrelated to
// the cleanup profiles in internal/config, which are user-authored bundles of
// categories and paths.
type Topic string

const (
	TopicSystemData Topic = "system-data"
)

func (t Topic) DisplayName() string {
	switch t {
	case TopicSystemData:
		return "System Data"
	default:
		return string(t)
	}
}
