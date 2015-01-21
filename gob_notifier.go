package hsup

type GobNotifier struct {
	Dd      DynoDriver
	AppName string
	OneShot bool

	Payload string
}

func (gn *GobNotifier) Notify() <-chan *Processes {
	out := make(chan *Processes)
	as := &AppSerializable{}
	as.FromBase64Gob(gn.Payload)

	procs := as.Procs(gn.AppName, gn.Dd, gn.OneShot)
	go func() {
		out <- procs
	}()

	return out
}
