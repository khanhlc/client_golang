// Copyright 2018 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prometheus

import (
	"fmt"
	"sort"

	"github.com/golang/protobuf/proto"

	dto "github.com/prometheus/client_model/go"
)

// WrapWith returns a Registerer wrapping the provided Registerer. Collectors
// registered with the returned Registerer will be registered with the wrapped
// Registerer in a modified way. The modified Collector adds the provided Labels
// to all Metrics it collects (as ConstLabels). The Metrics collected by the
// unmodified Collector must not have any of those labels on their own.
//
// WrapWith provides a way to add fixed labels to a subset of Collectors. It
// should not be used to add fixed labels to all metrics exposed.
//
// The Collector example demonstrates a use of WrapWith.
func WrapWith(labels Labels, reg Registerer) Registerer {
	return &wrappingRegisterer{
		wrappedRegisterer: reg,
		labels:            labels,
	}
}

// WrapWithPrefix returns a Registerer wrapping the provided Registerer. Collectors
// registered with the returned Registerer will be registered with the wrapped
// Registerer in a modified way. The modified Collector adds the provided prefix
// to the name of all Metrics it collects.
//
// WrapWithPrefix is useful to have one place to prefix all metrics of a
// sub-system. To make this work, register metrics of the sub-system with the
// wrapping Registerer returned by WrapWithPrefix.
func WrapWithPrefix(prefix string, reg Registerer) Registerer {
	return &wrappingRegisterer{
		wrappedRegisterer: reg,
		prefix:            prefix,
	}
}

type wrappingRegisterer struct {
	wrappedRegisterer Registerer
	prefix            string
	labels            Labels
}

func (r *wrappingRegisterer) Register(c Collector) error {
	return r.wrappedRegisterer.Register(&wrappingCollector{
		wrappedCollector: c,
		prefix:           r.prefix,
		labels:           r.labels,
	})
}

func (r *wrappingRegisterer) MustRegister(cs ...Collector) {
	for _, c := range cs {
		if err := r.Register(c); err != nil {
			panic(err)
		}
	}
}

func (r *wrappingRegisterer) Unregister(c Collector) bool {
	return r.wrappedRegisterer.Unregister(&wrappingCollector{
		wrappedCollector: c,
		prefix:           r.prefix,
		labels:           r.labels,
	})
}

type wrappingCollector struct {
	wrappedCollector Collector
	prefix           string
	labels           Labels
}

func (c *wrappingCollector) Collect(ch chan<- Metric) {
	wrappedCh := make(chan Metric)
	go func() {
		c.wrappedCollector.Collect(wrappedCh)
		close(wrappedCh)
	}()
	for m := range wrappedCh {
		ch <- &wrappingMetric{
			wrappedMetric: m,
			prefix:        c.prefix,
			labels:        c.labels,
		}
	}
}

func (c *wrappingCollector) Describe(ch chan<- *Desc) {
	wrappedCh := make(chan *Desc)
	go func() {
		c.wrappedCollector.Describe(wrappedCh)
		close(wrappedCh)
	}()
	for desc := range wrappedCh {
		ch <- wrapDesc(desc, c.prefix, c.labels)
	}
}

type wrappingMetric struct {
	wrappedMetric Metric
	prefix        string
	labels        Labels
}

func (m *wrappingMetric) Desc() *Desc {
	return wrapDesc(m.wrappedMetric.Desc(), m.prefix, m.labels)
}

func (m *wrappingMetric) Write(out *dto.Metric) error {
	if err := m.wrappedMetric.Write(out); err != nil {
		return err
	}
	if len(m.labels) == 0 {
		// No wrapping labels.
		return nil
	}
	for ln, lv := range m.labels {
		out.Label = append(out.Label, &dto.LabelPair{
			Name:  proto.String(ln),
			Value: proto.String(lv),
		})
	}
	sort.Sort(labelPairSorter(out.Label))
	return nil
}

func wrapDesc(desc *Desc, prefix string, labels Labels) *Desc {
	constLabels := Labels{}
	for _, lp := range desc.constLabelPairs {
		constLabels[*lp.Name] = *lp.Value
	}
	for ln, lv := range labels {
		if _, alreadyUsed := constLabels[ln]; alreadyUsed {
			return &Desc{
				fqName:          desc.fqName,
				help:            desc.help,
				variableLabels:  desc.variableLabels,
				constLabelPairs: desc.constLabelPairs,
				err:             fmt.Errorf("attempted wrapping with already existing label name %q", ln),
			}
		}
		constLabels[ln] = lv
	}
	// NewDesc will do remaining validations.
	newDesc := NewDesc(prefix+desc.fqName, desc.help, desc.variableLabels, constLabels)
	// Propagate errors if there was any. This will override any errer
	// created by NewDesc above, i.e. earlier errors get precedence.
	if desc.err != nil {
		newDesc.err = desc.err
	}
	return newDesc
}
