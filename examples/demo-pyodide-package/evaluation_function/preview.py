preview_invocation_count = 0


def preview_function(response, params):
    global preview_invocation_count
    preview_invocation_count += 1

    return {
        "preview": True,
        "response_preview": response,
        "rubric": params.get("rubric", "none"),
        "pyodide_runtime": True,
        "package_mode": True,
        "guest_invocation_count": preview_invocation_count,
        "fresh_namespace_ok": preview_invocation_count == 1,
    }
