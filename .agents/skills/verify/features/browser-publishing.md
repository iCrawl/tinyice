# Browser publishing

## Sub-features

Publish browser microphone audio, camera, screen, or a video file to a selected mount. Stop the publisher and verify listener cleanup and metadata clearing.

## How to get to it (user POV)

Open `/admin/golive`. The heading is **Browser Broadcast**. Source buttons are **Mic only**, **Camera**, **Screen**, and **Video file**. The broadcast button reads **GO LIVE**, then **CONNECTING...**, then **STOP BROADCAST**. Inspect the snapshot for the mount selector and device controls.

## Driving it with HTTP and T3 preview

Use a configured empty mount such as `/verify-mount`. Allow the browser's media permission prompt only for the disposable instance. Select a source and click **GO LIVE**. Capture the handshake to `/webrtc/source-offer`, the live status, and `/api/streams`. Open a real Player listener and verify audio or video bytes. For video, require `has_video` and the advertised dimensions. Click **STOP BROADCAST** and confirm both audio and video disappear.

Inspect Go Live's error message after a rejected handshake. Test a claimed AutoDJ mount and require the source rejection to leave its byte stream, metadata, and source label unchanged. On the integrated build, browser source replacement must wait for both old audio and video pumps before release. A delayed old disconnect must not remove the successor.

## Gotchas

Loopback HTTP qualifies for browser media APIs. Remote HTTP may not. A native preview host without microphone, camera, or screen capture can use a real generated audio-bearing video file through the file input. Upstream supports video-only capture without audio meters. Verify its actual video output separately from audio delivery. If no suitable source is available, report publishing as unverified. Do not change production source permissions or fabricate a WebRTC success response to bypass missing devices.
