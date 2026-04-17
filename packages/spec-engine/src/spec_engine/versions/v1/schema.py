REQUEST_DEFINITION_FIELDS = {
    "formatVersion", "kind", "id", "name", "description", "inputs", "request", "outputs"
}
TEST_SUITE_FIELDS = {
    "formatVersion", "kind", "info", "globals", "defaults", "tests", "id", "name", "description"
}
ENVIRONMENT_FIELDS = {"formatVersion", "kind", "name", "variables"}


def allowed_fields_for_kind(kind: str | None) -> set[str]:
    if kind == "requestDefinition":
        return REQUEST_DEFINITION_FIELDS
    if kind == "testSuite":
        return TEST_SUITE_FIELDS
    if kind == "environment":
        return ENVIRONMENT_FIELDS
    return set()
