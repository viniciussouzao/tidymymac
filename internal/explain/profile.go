package explain

type Profile string

const (
	ProfileSystemData Profile = "system-data"
)

func (p Profile) DisplayName() string {
	switch p {
	case ProfileSystemData:
		return "System Data"
	default:
		return string(p)
	}
}
