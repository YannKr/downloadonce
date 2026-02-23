"""Embed an invisible DWT-DCT-SVD watermark into an image.

Usage: python embed_watermark.py '<json_args>'
Args JSON: {"input_path": "...", "output_path": "...", "payload_hex": "...", "jpeg_quality": 92}
Output JSON: {"status": "ok"} or {"status": "error", "message": "..."}
"""
import sys
import json

def main():
    try:
        args = json.loads(sys.argv[1])
        input_path = args["input_path"]
        output_path = args["output_path"]
        payload_hex = args["payload_hex"]
        jpeg_quality = args.get("jpeg_quality", 92)

        import cv2
        from imwatermark import WatermarkEncoder

        img = cv2.imread(input_path)
        if img is None:
            print(json.dumps({"status": "error", "message": f"Failed to read image: {input_path}"}))
            sys.exit(1)

        payload_bytes = bytes.fromhex(payload_hex)

        encoder = WatermarkEncoder()
        encoder.set_watermark('bytes', payload_bytes)
        watermarked = encoder.encode(img, 'dwtDctSvd')

        # Determine output format from extension
        if output_path.lower().endswith(('.jpg', '.jpeg')):
            cv2.imwrite(output_path, watermarked, [int(cv2.IMWRITE_JPEG_QUALITY), jpeg_quality])
        elif output_path.lower().endswith('.png'):
            cv2.imwrite(output_path, watermarked, [int(cv2.IMWRITE_PNG_COMPRESSION), 3])
        elif output_path.lower().endswith('.webp'):
            cv2.imwrite(output_path, watermarked, [int(cv2.IMWRITE_WEBP_QUALITY), jpeg_quality])
        else:
            cv2.imwrite(output_path, watermarked)

        print(json.dumps({"status": "ok"}))

    except Exception as e:
        print(json.dumps({"status": "error", "message": str(e)}))
        sys.exit(1)

if __name__ == "__main__":
    main()
