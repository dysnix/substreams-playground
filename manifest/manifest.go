package manifest

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"
)

type Manifest struct {
	SpecVersion  string   `yaml:"specVersion"`
	Description  string   `yaml:"description"`
	CodeType     string   `yaml:"codeType"`
	GenesisBlock int      `yaml:"genesisBlock"`
	Streams      []Stream `yaml:"streams"`

	Graph *StreamsGraph `yaml:"-"`
}

type Stream struct {
	Name   string       `yaml:"name"`
	Kind   string       `yaml:"kind"`
	Code   Code         `yaml:"code"`
	Inputs []string     `yaml:"inputs"`
	Output StreamOutput `yaml:"output"`
}

type Code struct {
	File       string `yaml:"file"`
	Native     string `yaml:"native"`
	Content    []byte `yaml:"-"`
	Entrypoint string `yaml:"entrypoint"`
}

type StreamOutput struct {
	Type               string `yaml:"type"`
	StoreMergeStrategy string `yaml:"storeMergeStrategy"`
}

func (s *Stream) Signature(graph *StreamsGraph) []byte {
	buf := bytes.NewBuffer(nil)
	buf.WriteString(s.Kind)
	buf.Write(s.Code.Content)
	buf.Write([]byte(s.Code.Entrypoint))

	sort.Strings(s.Inputs)
	for _, input := range s.Inputs {
		buf.WriteString(input)
	}

	ancestors := graph.AncestorsOf(s.Name)
	for _, ancestor := range ancestors {
		sig := ancestor.Signature(graph)
		buf.Write(sig)
	}

	h := sha1.New()
	h.Write(buf.Bytes())

	return h.Sum(nil)
}

func (s *Stream) String() string {
	return s.Name
}

func New(path string) (m *Manifest, err error) {
	m, err = newWithoutLoad(path)
	if err != nil {
		return nil, err
	}

	for _, s := range m.Streams {
		if s.Code.Native == "" {
			cnt, err := ioutil.ReadFile(s.Code.File)
			if err != nil {
				return nil, fmt.Errorf("reading file %q: %w", s.Code.File, err)
			}
			s.Code.Content = cnt
		}
	}

	return
}
func newWithoutLoad(path string) (*Manifest, error) {
	_, m, err := DecodeYamlManifestFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("decoding yaml: %w", err)
	}

	switch m.CodeType {
	case "wasm/rust-v1", "native":
	default:
		return nil, fmt.Errorf("invalid value %q for 'codeType'", m.CodeType)
	}

	for _, s := range m.Streams {
		switch s.Kind {
		case "Mapper":
			if s.Output.Type == "" {
				return nil, fmt.Errorf("stream %q: missing 'output.type' for kind Mapper", s.Name)
			}
			if s.Code.Entrypoint == "" {
				s.Code.Entrypoint = "map"
			}
		case "StateBuilder":
			if s.Output.StoreMergeStrategy == "" {
				return nil, fmt.Errorf("stream %q: missing 'output.storeMergeStrategy' for kind StateBuilder", s.Name)
			}
			if s.Code.Entrypoint == "" {
				s.Code.Entrypoint = "build_state"
			}

		default:
			return nil, fmt.Errorf("stream %q: invalid kind %q", s.Name, s.Kind)
		}
	}

	graph, err := NewStreamsGraph(m.Streams)
	if err != nil {
		return nil, fmt.Errorf("computing streams graph: %w", err)
	}

	m.Graph = graph

	return m, nil
}

type StreamsGraph struct {
	streams map[string]Stream
	links   map[string][]Stream
}

func NewStreamsGraph(streams []Stream) (*StreamsGraph, error) {
	sg := &StreamsGraph{
		streams: map[string]Stream{},
		links:   map[string][]Stream{},
	}

	for _, stream := range streams {
		sg.streams[stream.Name] = stream
	}

	for _, stream := range streams {
		var links []Stream
		for _, input := range stream.Inputs {
			for _, streamPrefix := range []string{"stream:", "store:"} {
				if strings.HasPrefix(input, streamPrefix) {
					linkName := strings.TrimPrefix(input, streamPrefix)
					linkedStream, ok := sg.streams[linkName]
					if !ok {
						return nil, fmt.Errorf("stream %s does not exist", linkName)
					}
					links = append(links, linkedStream)
				}
			}

		}
		sg.links[stream.Name] = links
	}

	return sg, nil
}

func (g *StreamsGraph) StreamsFor(streamName string) ([]Stream, error) {
	thisStream, found := g.streams[streamName]
	if !found {
		return nil, fmt.Errorf("stream %q not found", streamName)
	}

	parents := g.ancestorsOf(streamName)
	return append(parents, thisStream), nil
}

//TODO: use this in pipeline and deduplicate everything
func (g *StreamsGraph) GroupedStreamsFor(streamName string) ([][]Stream, error) {
	thisStream, found := g.streams[streamName]
	if !found {
		return nil, fmt.Errorf("stream %q not found", streamName)
	}

	parents := g.groupedAncestorsOf(streamName)
	return append(parents, []Stream{thisStream}), nil
}

func (g *StreamsGraph) AncestorsOf(streamName string) []Stream {
	parents := g.ancestorsOf(streamName)
	return parents
}

func (g *StreamsGraph) GroupedAncestorsOf(streamName string) []Stream {
	parents := g.ancestorsOf(streamName)
	return parents
}

func (g *StreamsGraph) ancestorsOf(streamName string) []Stream {
	type streamWithTreeDepth struct {
		stream Stream
		depth  int
	}

	var dfs func(rootName string, depth int) []streamWithTreeDepth
	dfs = func(rootName string, depth int) []streamWithTreeDepth {
		var result []streamWithTreeDepth
		for _, link := range g.links[rootName] {
			result = append(result, streamWithTreeDepth{
				stream: link,
				depth:  depth,
			})

			result = append(result, dfs(link.Name, depth+1)...)
		}

		return result
	}

	parentsWithDepth := dfs(streamName, 0)

	//sort by depth in descending order
	sort.Slice(parentsWithDepth, func(i, j int) bool {
		if parentsWithDepth[i].depth == parentsWithDepth[j].depth {
			return parentsWithDepth[i].stream.Name < parentsWithDepth[j].stream.Name
		}
		return parentsWithDepth[i].depth > parentsWithDepth[j].depth
	})

	seen := map[string]struct{}{}
	var result []Stream
	for _, parent := range parentsWithDepth {
		if _, ok := seen[parent.stream.Name]; ok {
			continue
		}
		result = append(result, parent.stream)
		seen[parent.stream.Name] = struct{}{}
	}

	return result
}

func (g *StreamsGraph) groupedAncestorsOf(streamName string) [][]Stream {
	type streamWithTreeDepth struct {
		stream Stream
		depth  int
	}

	var dfs func(rootName string, depth int) []streamWithTreeDepth
	dfs = func(rootName string, depth int) []streamWithTreeDepth {
		var result []streamWithTreeDepth
		for _, link := range g.links[rootName] {
			result = append(result, streamWithTreeDepth{
				stream: link,
				depth:  depth,
			})

			result = append(result, dfs(link.Name, depth+1)...)
		}

		return result
	}

	parentsWithDepth := dfs(streamName, 0)

	//sort by depth in descending order
	sort.Slice(parentsWithDepth, func(i, j int) bool {
		if parentsWithDepth[i].depth == parentsWithDepth[j].depth {
			return parentsWithDepth[i].stream.Name < parentsWithDepth[j].stream.Name
		}
		return parentsWithDepth[i].depth > parentsWithDepth[j].depth
	})

	grouped := map[int][]Stream{}
	seen := map[string]struct{}{}
	for _, parent := range parentsWithDepth {
		if _, ok := seen[parent.stream.Name]; ok {
			continue
		}
		grouped[parent.depth] = append(grouped[parent.depth], parent.stream)
		seen[parent.stream.Name] = struct{}{}
	}

	result := make([][]Stream, len(grouped), len(grouped))
	for i, streams := range grouped {
		result[len(grouped)-1-i] = streams
	}

	return result
}
