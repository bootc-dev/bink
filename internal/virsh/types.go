package virsh

type QemuImgCreateOptions struct {
	Path          string
	Format        string
	BackingFile   string
	BackingFormat string
	Size          string
}
