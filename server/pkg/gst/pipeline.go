package gst

import "github.com/m1k1o/neko/server/pkg/types"

type Pipeline interface {
	Src() string
	Sample() chan types.Sample
	AttachAppsink(sinkName string)
	AttachAppsrc(srcName string)
	Play()
	Pause()
	Destroy()
	Push(buffer []byte)
	SetPropInt(binName string, prop string, value int) bool
	SetCapsFramerate(binName string, numerator, denominator int) bool
	SetCapsResolution(binName string, width, height int) bool
	EmitVideoKeyframe() bool
}
