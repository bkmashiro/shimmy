def normalize(value):
    return str(value).strip().lower()


def split_items(value):
    return {part.strip().lower() for part in str(value).split(',') if part.strip()}
