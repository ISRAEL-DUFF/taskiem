// Reading handwriting with AI (I3), on Settings beside Chat tools: a photo
// this phone or browser cannot read may be sent to Taskiem's AI, where an
// owner turned it on. Shown where this Taskiem can do it; only owners
// change it, everyone else sees it as it is.

import { useEffect, useState } from "react";
import { get, put } from "../api";
import { useAuth } from "../auth";
import { setAIPhotoSetting, type AIPhotoSetting } from "../lib/photoAI";
import { ErrorBox, useAction, useLoad } from "../ui";

export function AIPhotoReadingSwitch() {
  const { me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const view = useLoad(() => get<AIPhotoSetting>("/v1/settings/ai-photo-reading"), []);
  const act = useAction();
  // What was just chosen shows at once; the saved value once it is back.
  const [chosen, setChosen] = useState<boolean | null>(null);
  // Come from a photo's "Turn on reading with AI": shown once it is here.
  const shown = !!view.data?.available;
  useEffect(() => {
    if (shown && window.location.hash === "#ai-photo-reading") document.getElementById("ai-photo-reading")?.scrollIntoView({ block: "center" });
  }, [shown]);
  if (!view.data?.available) return view.error ? <ErrorBox error={view.error} /> : null;
  const on = chosen ?? view.data.ai_photo_reading;
  return (
    <div className="card" id="ai-photo-reading" data-testid="settings-ai-photo-reading">
      <h2 style={{ marginTop: 0 }}>Reading photos</h2>
      <label>
        <input
          type="checkbox"
          checked={on}
          disabled={!owner || act.busy}
          onChange={(e) => {
            const next = e.target.checked;
            setChosen(next);
            void act.run(async () => {
              try {
                setAIPhotoSetting(await put<AIPhotoSetting>("/v1/settings/ai-photo-reading", { ai_photo_reading: next }));
              } catch (err) {
                setChosen(null);
                throw err;
              }
            }, "Saved");
          }}
          data-testid="ai-photo-reading"
        />{" "}
        Read handwriting with AI
      </label>
      <p className="hint">
        When the phone or browser cannot read a photo of account details, such as handwriting, it may be sent to Taskiem's AI to read. The photo is not kept, and its words go back only to whoever asked. Each photo read this way uses some of this month's AI allowance.
      </p>
      {!owner && (
        <p className="hint" data-testid="ai-photo-reading-owner-only">
          Only an owner can change this.
        </p>
      )}
      <ErrorBox error={act.error} />
    </div>
  );
}
