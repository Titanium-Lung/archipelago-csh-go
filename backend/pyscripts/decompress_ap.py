import sys
import json
import zlib
sys.path.insert(0, "Archipelago-0.6.7")
from Utils import restricted_loads # type: ignore

class ArchipelagoJSONEncoder(json.JSONEncoder):
    def default(self, o):
        if isinstance(o, (set, frozenset)):
            return list(o)
        if isinstance(o, bytes):
            return o.decode("utf-8", errors="replace")
        return super().default(o)

def main():
    path = sys.argv[1]
    with open(path, "rb") as f:
        data = f.read()
        decoded_arch = restricted_loads(zlib.decompress(data[1:]))

        # print(type(decoded_arch), decoded_arch.keys())

        print(json.dumps(decoded_arch, cls=ArchipelagoJSONEncoder))

if __name__ == "__main__":
    main()