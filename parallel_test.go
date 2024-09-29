package rediscache

func runAllAndWait(funcs ...func()) {
	done := make(chan struct{})
	for _, f := range funcs {
		go func(f func()) {
			f()
			done <- struct{}{}
		}(f)
	}
	for range funcs {
		<-done
	}
}
