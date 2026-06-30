package dependent

import "example.test/upstream"

func Hello() string {
	return upstream.Greeting() + " world"
}
