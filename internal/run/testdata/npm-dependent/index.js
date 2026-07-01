const upstream = require("downstream-fixture-upstream");

exports.observed = function observed() {
  return upstream.answer();
};
