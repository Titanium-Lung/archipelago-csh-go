import sys
import json
import zlib
sys.path.insert(0, "Archipelago-0.6.7")
from Utils import restricted_loads # type: ignore

def make_json_safe(obj):
    if isinstance(obj, dict):
        new_dict = {}
        for k, v in obj.items():
            if isinstance(k, tuple):
                key = ",".join(str(x) for x in k)
            else:
                key = str(k) if not isinstance(k, (str, int, float, bool)) or k is None else k
            new_dict[key] = make_json_safe(v)
        return new_dict
    elif isinstance(obj, (list, tuple)):
        return [make_json_safe(item) for item in obj]
    elif isinstance(obj, set):
        return [make_json_safe(item) for item in obj]
    elif isinstance(obj, bytes):
        return obj.decode("utf-8", errors="replace")
    else:
        return obj

def main():
    path = sys.argv[1]
    with open(path, "rb") as f:
        data = f.read()
        decoded_apsave = restricted_loads(zlib.decompress(data))

        safe_data = make_json_safe(decoded_apsave)

        # with open("./uploads/sample-apsave.txt", "w") as f:
        #     f.write(str(safe_data))

        print(json.dumps(safe_data))

if __name__ == "__main__":
    main()