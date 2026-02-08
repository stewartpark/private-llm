package assets

// Logo frames for dashboard animation.
// Frame 0 = idle, Frames 1-3 = processing animation.
var DashFrames = [4]string{
	// Frame 0: idle
	`  .---. .---. .---.
  |///| | >_ | |o-o|
  '---' '---' '---'
     \  (o.o)  /
      '=[===]='`,
	// Frame 1: processing
	`  .---. .---. .---.
  |/\/| | >_ | |o-o|
  '---' '---' '---'
     \  (O.O)  /
      '=[=~=]='`,
	// Frame 2: processing
	`  .---. .---. .---.
  |///| | >> | |o-o|
  '---' '---' '---'
     \  (o.O)  /
      '=[~~=]='`,
	// Frame 3: processing
	`  .---. .---. .---.
  |\/\| | >_ | |o-o|
  '---' '---' '---'
     \  (O.o)  /
      '=[=~~]='`,
}

// BootLogo is the full logo shown on the boot screen.
const BootLogo = `
          .---. .---. .---.
          |///| | >_ | |o-o|
          '---' '---' '---'
            \\    |    //
         /\  \\   |   //  /\
        (  '. '.---..' .'  )
        (   / o     o \   )
         ) (    ._.    ) (
        (   \  '---'  /   )
         '.  '-------'  .'
          [____/===\____]`

// GetBootLogo returns the full boot screen logo.
func GetBootLogo() string {
	return BootLogo
}

// GetDashFrame returns the logo frame at the given index (0-3).
func GetDashFrame(frame int) string {
	return DashFrames[frame%len(DashFrames)]
}
