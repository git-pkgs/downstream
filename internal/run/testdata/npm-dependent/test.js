const assert = require("node:assert/strict");
const dependent = require("./index");

assert.equal(dependent.observed(), "good");
