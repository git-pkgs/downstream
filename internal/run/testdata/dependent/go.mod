module example.test/dependent

go 1.22

require example.test/upstream v0.0.0

replace example.test/upstream => ../upstream
