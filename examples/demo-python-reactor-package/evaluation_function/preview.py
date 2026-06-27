def preview_function(response, params=None):
    if params is None:
        params = {}
    return {
        "preview": f"python-reactor package preview: {response}",
        "params_keys": sorted(params.keys()),
    }
