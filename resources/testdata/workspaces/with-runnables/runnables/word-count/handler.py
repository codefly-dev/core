def handle(context, input):
    return {"count": len(input["text"].split()), "longest": None}
