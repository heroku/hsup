package hsup

type GobNotifier struct {
	Payload string
}

func (gn *GobNotifier) Notify() <-chan *Processes {
	out := make(chan *Processes)
	go func() {
		hs := &Startup{}
		hs.FromBase64Gob(gn.Payload)
		procs := hs.Procs()
		out <- procs
	}()

	return out
}
