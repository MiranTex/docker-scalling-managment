"use client";

import { useEffect, useRef, useState } from "react";

type Listener = (message: string) => void;
let listener: Listener | null = null;

export function toast(message: string) {
  listener?.(message);
}

// Uma transição (não keyframes): se disparar duas vezes seguidas, retoma do valor atual.
export default function Toaster() {
  const [message, setMessage] = useState("");
  const [visible, setVisible] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    listener = (m) => {
      setMessage(m);
      setVisible(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setVisible(false), 1800);
    };
    return () => {
      listener = null;
      clearTimeout(timer.current);
    };
  }, []);

  return (
    <div className={`toast${visible ? " visible" : ""}`} role="status" aria-live="polite">
      {message}
    </div>
  );
}
