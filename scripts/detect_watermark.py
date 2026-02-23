"""Detect an invisible DWT-DCT-SVD watermark from an image.

Usage: python detect_watermark.py '<json_args>'
Args JSON: {"input_path": "...", "payload_length": 16}
  payload_length is in bytes (will be converted to bits for the decoder).
Output JSON: {"status": "ok", "payload_hex": "..."} or {"status": "error", "message": "..."}
"""
import sys
import json

def main():
    try:
        args = json.loads(sys.argv[1])
        input_path = args["input_path"]
        payload_length = args.get("payload_length", 16)

        import cv2
        from imwatermark import WatermarkDecoder

        img = cv2.imread(input_path)
        if img is None:
            print(json.dumps({"status": "error", "message": f"Failed to read image: {input_path}"}))
            sys.exit(1)

        # WatermarkDecoder expects bit count for 'bytes' mode
        decoder = WatermarkDecoder('bytes', payload_length * 8)
        payload = decoder.decode(img, 'dwtDctSvd')

        print(json.dumps({"status": "ok", "payload_hex": payload.hex()}))

    except Exception as e:
        print(json.dumps({"status": "error", "message": str(e)}))
        sys.exit(1)

if __name__ == "__main__":
    main()
