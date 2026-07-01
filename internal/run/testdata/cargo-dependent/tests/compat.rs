#[test]
fn observes_upstream_answer() {
    assert_eq!(downstream_fixture_dependent::observed(), "good");
}
